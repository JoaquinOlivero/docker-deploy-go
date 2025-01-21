package main

import (
	"docker-deploy/internal/router"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	os.Setenv("DOCKER_API_VERSION", "1.44")

	router := router.NewRouter()

	log.Println("Server Listening on http://localhost:3002")
	handler := cors.Default().Handler(router)
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"http://localhost", "https://github.com"},
		AllowedMethods: []string{"GET", "POST"},
		// Debug:            true,
	})
	handler = c.Handler(handler)

	log.Fatal(http.ListenAndServe(":3002", handler))

}
