package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"money-manager-server/internal/apperrors"
	"money-manager-server/internal/model"
	"money-manager-server/internal/repository"

	"github.com/golang-jwt/jwt/v5"
)

type tokenClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

func (s *Service) issueToken(user model.User) (string, error) {
	now := s.now().UTC()
	claims := tokenClaims{
		Email: user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   strconv.Itoa(user.ID),
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", apperrors.Internal(fmt.Errorf("sign access token: %w", err))
	}
	return token, nil
}

func (s *Service) ParseUserID(rawToken string) (int, error) {
	claims, err := s.parseCurrentToken(rawToken)
	if err == nil {
		return userIDFromClaims(claims)
	}
	if s.legacyAcceptUntil.IsZero() || !s.now().UTC().Before(s.legacyAcceptUntil) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	legacyClaims, legacyErr := s.parseLegacyToken(rawToken)
	if legacyErr != nil || legacyClaims.Issuer != "" || len(legacyClaims.Audience) != 0 ||
		legacyClaims.ExpiresAt == nil || legacyClaims.ExpiresAt.Time.After(s.legacyAcceptUntil) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	return userIDFromClaims(legacyClaims)
}

func (s *Service) parseCurrentToken(rawToken string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid current token")
	}
	return claims, nil
}

func (s *Service) parseLegacyToken(rawToken string) (*tokenClaims, error) {
	claims := &tokenClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return s.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !token.Valid {
		return nil, errors.New("invalid legacy token")
	}
	return claims, nil
}

func userIDFromClaims(claims *tokenClaims) (int, error) {
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	}
	return userID, nil
}

func (s *Service) Authenticate(ctx context.Context, rawToken string) (int, error) {
	userID, err := s.ParseUserID(rawToken)
	if err != nil {
		return 0, err
	}
	if _, err := s.store.GetUser(ctx, userID); errors.Is(err, repository.ErrNotFound) {
		return 0, apperrors.Unauthorized("invalid or expired access token")
	} else if err != nil {
		return 0, apperrors.Internal(fmt.Errorf("authenticate user: %w", err))
	}
	return userID, nil
}
