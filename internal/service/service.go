package service

import (
	"context"
	"errors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	Repo     *repository.Repository
	dbCloser interface{ Close() }
	secret   []byte
}

func New(ctx context.Context, cfg config.Config) (*Service, error) {
	db, err := repository.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := repository.Migrate(ctx, db); err != nil {
		return nil, err
	}
	return &Service{Repo: repository.New(db), dbCloser: db, secret: []byte(cfg.JWTSecret)}, nil
}
func (s *Service) Close() { s.dbCloser.Close() }
func (s *Service) Register(ctx context.Context, req model.AuthRequest) (model.AuthResponse, error) {
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		return model.AuthResponse{}, errors.New("email and password are required")
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id, err := s.Repo.CreateUser(ctx, req.Email, string(h))
	if err != nil {
		return model.AuthResponse{}, err
	}
	if err := s.Repo.EnsureDefaultCategories(ctx, id); err != nil {
		return model.AuthResponse{}, err
	}
	tok, _ := s.token(id, req.Email)
	return model.AuthResponse{Token: tok, User: model.User{ID: id, Email: req.Email}}, nil
}
func (s *Service) Login(ctx context.Context, req model.AuthRequest) (model.AuthResponse, error) {
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		return model.AuthResponse{}, errors.New("email and password are required")
	}
	id, hash, err := s.Repo.FindUser(ctx, req.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.AuthResponse{}, errors.New("invalid credentials")
		}
		return model.AuthResponse{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return model.AuthResponse{}, errors.New("invalid credentials")
	}
	if err := s.Repo.EnsureDefaultCategories(ctx, id); err != nil {
		return model.AuthResponse{}, err
	}
	tok, _ := s.token(id, req.Email)
	return model.AuthResponse{Token: tok, User: model.User{ID: id, Email: req.Email}}, nil
}

func (s *Service) ListCategories(ctx context.Context, userID int, typ string) ([]model.Category, error) {
	if !validTransactionType(typ) {
		return nil, errors.New("type must be expense or income")
	}
	if err := s.Repo.EnsureDefaultCategories(ctx, userID); err != nil {
		return nil, err
	}
	return s.Repo.ListCategories(ctx, userID, typ)
}

func (s *Service) CreateCategory(ctx context.Context, userID int, req model.CategoryRequest) (model.Category, error) {
	req.Type = strings.TrimSpace(req.Type)
	req.Name = strings.TrimSpace(req.Name)
	if !validTransactionType(req.Type) {
		return model.Category{}, errors.New("type must be expense or income")
	}
	if req.Name == "" {
		return model.Category{}, errors.New("category name is required")
	}
	if len(req.Name) > 40 {
		return model.Category{}, errors.New("category name must be 40 characters or less")
	}
	return s.Repo.CreateCategory(ctx, userID, req)
}

func (s *Service) DeleteCategory(ctx context.Context, userID, id int) error {
	return s.Repo.DeleteCategory(ctx, userID, id)
}

func (s *Service) ExportTransactions(ctx context.Context, userID int, from, to string) ([]model.Transaction, error) {
	fromDate, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, errors.New("from must use YYYY-MM-DD")
	}
	toDate, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, errors.New("to must use YYYY-MM-DD")
	}
	if fromDate.After(toDate) {
		return nil, errors.New("from must be before or equal to to")
	}
	return s.Repo.ExportTransactions(ctx, userID, from, to)
}

func (s *Service) token(id int, email string) (string, error) {
	c := jwt.MapClaims{"sub": strconv.Itoa(id), "email": email, "exp": time.Now().Add(24 * time.Hour).Unix()}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
}
func (s *Service) ParseUserID(token string) (int, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (any, error) { return s.secret, nil })
	if err != nil {
		return 0, err
	}
	sub, _ := t.Claims.(jwt.MapClaims)["sub"].(string)
	return strconv.Atoi(sub)
}

func validTransactionType(typ string) bool {
	return typ == "expense" || typ == "income"
}
