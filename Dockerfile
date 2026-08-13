FROM golang:1.26 AS builder

WORKDIR /src

COPY . .

RUN CGO_ENABLED=0 go build -o /out/gopull


FROM gcr.io/distroless/static

COPY --from=builder /out/gopull /usr/local/bin/gopull

ENTRYPOINT ["gopull"]
