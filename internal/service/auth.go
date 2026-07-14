package service

import (
	"context"
	"errors"
	"fmt"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

func (s *Service) Register(ctx context.Context, request model.AuthRequest) (model.AuthResponse, error) {
	email, err := normalizeEmail(request.Email)
	if err != nil {
		return model.AuthResponse{}, err
	}
	if err := validatePassword(request.Password); err != nil {
		return model.AuthResponse{}, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("hash password: %w", err))
	}
	user, err := s.store.RegisterUser(ctx, email, string(passwordHash))
	if errors.Is(err, repository.ErrConflict) {
		return model.AuthResponse{}, apperrors.Conflict("email is already registered")
	}
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("register user: %w", err))
	}
	token, err := s.issueToken(user)
	if err != nil {
		return model.AuthResponse{}, err
	}
	return model.AuthResponse{Token: token, User: user}, nil
}

func (s *Service) Login(ctx context.Context, request model.AuthRequest) (model.AuthResponse, error) {
	email, err := normalizeLoginEmail(request.Email)
	if err != nil {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if request.Password == "" || len([]byte(request.Password)) > maximumPasswordBytes {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}

	record, err := s.store.FindUserByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("find user: %w", err))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(request.Password)); err != nil {
		return model.AuthResponse{}, apperrors.Unauthorized("invalid credentials")
	}
	if err := s.store.EnsureDefaultCategories(ctx, record.User.ID); err != nil {
		return model.AuthResponse{}, apperrors.Internal(fmt.Errorf("ensure default categories: %w", err))
	}
	token, err := s.issueToken(record.User)
	if err != nil {
		return model.AuthResponse{}, err
	}
	return model.AuthResponse{Token: token, User: record.User}, nil
}

func (s *Service) GetMe(ctx context.Context, userID int) (model.User, error) {
	if err := validateID(userID); err != nil {
		return model.User{}, err
	}
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.User{}, apperrors.NotFound("user not found")
	}
	if err != nil {
		return model.User{}, apperrors.Internal(fmt.Errorf("get user: %w", err))
	}
	return user, nil
}

func (s *Service) DeleteMe(ctx context.Context, userID int) error {
	if err := validateID(userID); err != nil {
		return err
	}
	sessions, err := s.store.ListOpenBankingProviderSessions(ctx, userID)
	if err != nil {
		return apperrors.Internal(fmt.Errorf("list bank sessions before user deletion: %w", err))
	}
	if len(sessions) > 0 {
		client, err := s.requireOpenBanking()
		if err != nil {
			return err
		}
		for _, sessionID := range sessions {
			if err := client.DeleteSession(ctx, sessionID, enableBankingEmptyPSUHeaders()); err != nil && !providerSessionAlreadyGone(err) {
				return mapOpenBankingProviderError("revoke session before user deletion", err)
			}
		}
	}
	err = s.store.DeleteUser(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperrors.NotFound("user not found")
	}
	if err != nil {
		return apperrors.Internal(fmt.Errorf("delete user: %w", err))
	}
	return nil
}
