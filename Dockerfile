FROM golang:1.18 AS builder

COPY . /app
WORKDIR /app

RUN CGO_ENABLED=0 go build -o /app/truestreet -trimpath

FROM gcr.io/distroless/static

COPY --from=builder /app/truestreet /truestreet

EXPOSE 1760

ENTRYPOINT [ "/truestreet" ]