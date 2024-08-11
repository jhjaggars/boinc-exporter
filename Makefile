build:
	go mod tidy
	go build

image: build
	podman build -t quay.io/jhjaggars/boinc-exporter:latest .

push: image
	podman push quay.io/jhjaggars/boinc-exporter:latest
