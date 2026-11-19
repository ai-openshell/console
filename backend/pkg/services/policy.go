package services

import (
	openshell "github.com/ai-openshell/sdk-go/openshell/v1"
)

type PolicyServiceInterface interface {
	openshell.PolicyInterface
}

type PolicyService struct {
	openshell.PolicyInterface
}

func NewPolicyService(client openshell.PolicyInterface) *PolicyService {
	return &PolicyService{PolicyInterface: client}
}
