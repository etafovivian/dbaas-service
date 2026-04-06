module github.com/etafovivian/dbaas-service

go 1.26

require (
	github.com/etafovivian/shared v0.0.0
	github.com/jackc/pgx/v5 v5.8.0
	github.com/nats-io/nats.go v1.49.0
	github.com/prometheus/client_golang v1.23.2
)

replace github.com/etafovivian/shared => ../etafovivian-shared
