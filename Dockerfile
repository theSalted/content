FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/content ./cmd/content
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=go-build --chown=65532:65532 /out/content /content
COPY --chown=65532:65532 deploy/certs/supabase-ca.crt /etc/content/supabase-ca.crt
USER 65532:65532
ENV CONTENT_LISTEN=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/content"]
CMD ["serve"]
