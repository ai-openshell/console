package services

import (
	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

type ConfigServiceInterface interface {
	openshell.ConfigInterface
}

type ConfigService struct {
	openshell.ConfigInterface
}

func NewConfig(client openshell.ConfigInterface) *ConfigService {
	return &ConfigService{ConfigInterface: client}
}
