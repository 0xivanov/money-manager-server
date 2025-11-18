package main

import (
	"log"
	"net/http"

	"github.com/0xivanov/money-manager/api"
	"github.com/0xivanov/money-manager/db"
)

func main() {
	if err := db.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.CloseDB()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/openapi.yaml", api.ServeOpenAPISpec)
	mux.HandleFunc("GET /openapi.yaml", api.ServeOpenAPISpec)
	mux.HandleFunc("GET /api/docs", api.ServeSwaggerUI)
	mux.HandleFunc("GET /swagger", api.ServeSwaggerUI)
	mux.HandleFunc("GET /docs", api.ServeSwaggerUI)

	mux.HandleFunc("GET /users", api.GetAllUsers)
	mux.HandleFunc("POST /users", api.CreateUser)
	mux.HandleFunc("GET /users/{id}", api.GetUser)
	mux.HandleFunc("PUT /users/{id}", api.UpdateUser)
	mux.HandleFunc("DELETE /users/{id}", api.DeleteUser)

	mux.HandleFunc("GET /spending", api.GetAllSpending)
	mux.HandleFunc("POST /spending", api.CreateSpending)
	mux.HandleFunc("GET /spending/{id}", api.GetSpending)
	mux.HandleFunc("PUT /spending/{id}", api.UpdateSpending)
	mux.HandleFunc("DELETE /spending/{id}", api.DeleteSpending)

	mux.HandleFunc("GET /income", api.GetAllIncome)
	mux.HandleFunc("POST /income", api.CreateIncome)
	mux.HandleFunc("GET /income/{id}", api.GetIncome)
	mux.HandleFunc("PUT /income/{id}", api.UpdateIncome)
	mux.HandleFunc("DELETE /income/{id}", api.DeleteIncome)

	port := ":8080"
	log.Printf("Server starting on port %s", port)
	log.Fatal(http.ListenAndServe(port, mux))
}
