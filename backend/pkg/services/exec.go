package services

import (
	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

type ExecServiceInterface interface {
	openshell.ExecInterface
}

type ExecService struct {
	openshell.ExecInterface
}

func NewExecService(client openshell.ExecInterface) *ExecService {
	return &ExecService{ExecInterface: client}
}
