FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /skksync . \
 && curl -fsSL -o /SKK-JISYO.L.gz https://skk-dev.github.io/dict/SKK-JISYO.L.gz

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /skksync /skksync
COPY --from=build /SKK-JISYO.L.gz /SKK-JISYO.L.gz
ENV SKK_JISYO_L=/SKK-JISYO.L.gz
EXPOSE 8080
ENTRYPOINT ["/skksync"]
