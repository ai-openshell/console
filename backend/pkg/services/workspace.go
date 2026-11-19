package services

import openshell "github.com/ai-openshell/sdk-go/openshell/v1"

type WorkspaceServiceInterface interface {
	openshell.WorkspaceInterface
}

type WorkspaceService struct {
	openshell.WorkspaceInterface
}

func NewWorkspaceService(client openshell.WorkspaceInterface) WorkspaceServiceInterface {
	return &WorkspaceService{WorkspaceInterface: client}
}
