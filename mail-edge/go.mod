module tempmail/mail-edge

go 1.23

require (
	github.com/emersion/go-smtp v0.21.2
	github.com/redis/go-redis/v9 v9.5.1
	go.uber.org/zap v1.27.0
	tempmail/shared v0.0.0-00010101000000-000000000000
)

replace tempmail/shared => ../shared
