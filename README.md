# Deploy Docker Go

Continuous deployment tool written in Go that restarts and updates docker containers when requested, thus automatically delivering code-changes to end-users.

This tool is specifically tailored to my current needs. 

It reads the docker-compose.yml file used for the application that I'd like to check for updates and proceeds to remove the container and create a new one with the updated image. 

