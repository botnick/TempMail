module tempmail/api

go 1.23

require (
	github.com/aws/aws-sdk-go-v2 v1.26.1
	github.com/aws/aws-sdk-go-v2/config v1.27.11
	github.com/aws/aws-sdk-go-v2/credentials v1.17.11
	github.com/aws/aws-sdk-go-v2/service/s3 v1.53.1
	github.com/gofiber/fiber/v2 v2.52.4
	github.com/google/uuid v1.6.0
	github.com/microcosm-cc/bluemonday v1.0.26
	go.uber.org/zap v1.27.0
	tempmail/shared v0.0.0-00010101000000-000000000000
)

replace tempmail/shared => ../shared
