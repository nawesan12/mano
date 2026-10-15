# Binario estático, imagen mínima. Para el VPS: docker build -t mano-worker apps/worker
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mano ./cmd/mano

FROM gcr.io/distroless/static-debian12
COPY --from=build /mano /mano
# zona horaria y certificados vienen en distroless/static
EXPOSE 8787
ENTRYPOINT ["/mano"]
