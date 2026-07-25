package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	AMQPURL   string
	QueueName string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		AMQPURL:   os.Getenv("AMQP_URL"),
		QueueName: os.Getenv("AMQP_QUEUE_NAME"),
	}

	var missing []string
	if cfg.AMQPURL == "" {
		missing = append(missing, "AMQP_URL")
	}
	if cfg.QueueName == "" {
		missing = append(missing, "AMQP_QUEUE_NAME")
	}

	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
