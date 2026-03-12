package apiutil

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
	"tempmail/shared/logger"
)

// StandardError represents a standard API error structure
type StandardError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// SendError sends a standardized JSON error response
func SendError(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(StandardError{
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{
			Code:    code,
			Message: message,
		},
	})
}

// SendErrorLog is a shorthand that also logs the error details
func SendErrorLog(c *fiber.Ctx, status int, code, message string, err error) error {
	logger.Log.Error(message, zap.Error(err), zap.String("code", code), zap.String("ip", c.IP()))
	return SendError(c, status, code, message)
}
