package app

import (
	"context"
	"log"
	"money-manager-server/internal/config"
	"money-manager-server/internal/router"
	"money-manager-server/internal/service"
	"net/http"
)

func Run() {
	cfg := config.Load()
	svc, err := service.New(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Close()

	h := router.Build(svc)
	log.Printf("Money Manager API listening on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, h); err != nil {
		log.Fatal(err)
	}
}
