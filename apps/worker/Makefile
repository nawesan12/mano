.PHONY: run test build watch

run:            ## arranca el worker con el .env de la raíz
	go run ./cmd/mano

test:           ## vet + tests con detector de carreras
	go vet ./... && go test -race ./...

build:          ## binario para Linux (VPS)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o bin/mano ./cmd/mano

watch:          ## registra el watch de Gmail a mano
	go run ./cmd/mano -gmail-watch
