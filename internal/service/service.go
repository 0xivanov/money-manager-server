package service

import (
	"context"
	"errors"
	"money-manager-server/internal/config"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	h, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id, err := s.Repo.CreateUser(ctx, req.Email, string(h))
	if err != nil {
		return model.AuthResponse{}, err
	}
	tok, _ := s.token(id, req.Email)
	return model.AuthResponse{Token: tok, User: model.User{ID: id, Email: req.Email}}, nil
}
func (s *Service) Login(ctx context.Context, req model.AuthRequest) (model.AuthResponse, error) {
	id, hash, err := s.Repo.FindUser(ctx, req.Email)
	if err != nil {
		return model.AuthResponse{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return model.AuthResponse{}, errors.New("invalid credentials")
	}
	tok, _ := s.token(id, req.Email)
	return model.AuthResponse{Token: tok, User: model.User{ID: id, Email: req.Email}}, nil
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
