package service

import (
	"errors"
	"net/http"

	"money-manager-server/internal/config"
	"money-manager-server/internal/push"
)

func (s *Service) configurePush(cfg config.Config) {
	s.pushSenders = make(map[string]notificationSender)
	if cfg.APNSPrivateKey != nil {
		client, err := push.NewAPNSClient(push.APNSConfig{
			KeyID: cfg.APNSKeyID, TeamID: cfg.APNSTeamID, BundleID: cfg.APNSBundleID,
			PrivateKey: cfg.APNSPrivateKey, HTTPClient: &http.Client{Timeout: cfg.APNSRequestTimeout},
		})
		if err != nil {
			s.pushError = errors.Join(s.pushError, err)
		} else {
			s.pushSenders["ios"] = client
			s.pushPlatforms = append(s.pushPlatforms, "ios")
		}
	}
	if cfg.FCMPrivateKey != nil {
		client, err := push.NewFCMClient(push.FCMConfig{
			ProjectID: cfg.FCMProjectID, ClientEmail: cfg.FCMClientEmail,
			PrivateKey: cfg.FCMPrivateKey, HTTPClient: &http.Client{Timeout: cfg.FCMRequestTimeout},
		})
		if err != nil {
			s.pushError = errors.Join(s.pushError, err)
		} else {
			s.pushSenders["android"] = client
			s.pushPlatforms = append(s.pushPlatforms, "android")
		}
	}
}
