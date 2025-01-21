package router

import (
	"bufio"
	"context"
	"docker-deploy/internal/middleware"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/cli"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

func NewRouter() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /update-containers", UpdateContainers)

	logging := middleware.Logging(mux)
	auth := middleware.Auth(logging)
	return auth
}

func UpdateContainers(w http.ResponseWriter, r *http.Request) {
	composeFilePath := "data/docker-compose.yml"
	projectName := "gleam"

	options, err := cli.NewProjectOptions(
		[]string{composeFilePath},
		cli.WithOsEnv,
		cli.WithDotEnv,
		cli.WithName(projectName),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	project, err := cli.ProjectFromOptions(options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	apiClient, err := client.NewClientWithOpts(
		client.WithHost("unix:///var/run/docker.sock"),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer apiClient.Close()

	authConfig := registry.AuthConfig{
		ServerAddress: "ghcr.io",
		Username:      "joaquinolivero",
		Password:      os.Getenv("TOKEN"),
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	var i int
	for _, srv := range project.Services {
		if srv.ContainerName == "redis-gleam-prod" {
			continue
		}

		var imgID, ctrID string

		containers, err := apiClient.ContainerList(context.Background(), container.ListOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, ctr := range containers {
			if ctr.Names[0] == "/"+srv.ContainerName {
				imgID = ctr.ImageID
				ctrID = ctr.ID
				break
			}
		}

		// Stop and remove container
		err = apiClient.ContainerRemove(context.Background(), ctrID, container.RemoveOptions{
			Force: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// check if image already exists in the system
		// pull image if the image does not exist in the system
		// to pull the image from the private repo
		// it's necessary to login first
		// Pull image
		out, err := apiClient.ImagePull(context.Background(), srv.Image, image.PullOptions{RegistryAuth: authStr})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		// Check if the current img is up to date. This is can be found out when pulling the image from the repository
		currentImg, err := imageIsUpToDate(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Env variables
		var envVars []string

		for key, v := range srv.Environment {
			envVar := fmt.Sprintf("%v=%v", key, *v)
			envVars = append(envVars, envVar)
		}

		// Container configuration
		contConfig := &container.Config{
			Image: srv.Image,
			User:  srv.User,
			Env:   envVars,
		}

		// Host configuration
		hostConfig := &container.HostConfig{
			Binds: []string{
				srv.Volumes[0].String(),
			},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(srv.Restart)},
		}

		// Network configuration
		// Endpoints
		endConfig := make(map[string]*network.EndpointSettings, len(srv.Networks))
		var ipv4Address string

		for key, v := range srv.Networks {
			endConfig[key] = &network.EndpointSettings{
				IPAddress: v.Ipv4Address,
			}

			ipv4Address = v.Ipv4Address
		}

		netConfig := &network.NetworkingConfig{
			EndpointsConfig: endConfig,
		}

		resp, err := apiClient.ContainerCreate(context.Background(), contConfig, hostConfig, netConfig, nil, srv.ContainerName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := apiClient.ContainerStart(context.Background(), resp.ID, container.StartOptions{}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Check if the server started and do a health check to the specific endpoint
		err = serverCheck(resp.ID, ipv4Address, srv.Name, apiClient)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if i == 0 {
			i++
			continue
		}

		// If a new image was used to create and run the container it's necessary to remove the older one.
		if !currentImg {
			_, err = apiClient.ImageRemove(context.Background(), imgID, image.RemoveOptions{Force: true})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

}

func serverCheck(id, ip, name string, client *client.Client) error {
	// First check if the container is running. Then, check the health endpoint from the server
	attempts := 10
healthCheck:
	for {
		if attempts == 0 {
			err := errors.New("timeout waiting for health check response")
			return err
		}

		// Inspect container
		containerJSON, err := client.ContainerInspect(context.Background(), id)
		if err != nil {
			return fmt.Errorf("error inspecting container: %v", err)
		}

		// Check if container is running
		if containerJSON.State.Running {
			// Make a request the to the server's health endpoint
			resp, err := http.Get("http://" + ip + ":3001/health")
			if err != nil {
				log.Println(err)
				time.Sleep(1000 * time.Millisecond)
				attempts--
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				log.Printf("%v is running and healthy", name)
				break healthCheck
			}
		}
	}

	return nil
}

func imageIsUpToDate(rc io.ReadCloser) (bool, error) {
	// Method 2: Using bufio for more efficient reading
	defer rc.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(rc)

	for scanner.Scan() {
		builder.WriteString(scanner.Text() + "\n")

		imgStatus := `{"status":"Status: Image is up to date for ghcr.io/joaquinolivero/gleam:linux-arm"}`
		if strings.Contains(builder.String(), imgStatus) {
			log.Println("image is already up to date")
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, err
	}

	return false, nil
}
