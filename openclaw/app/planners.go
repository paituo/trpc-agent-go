//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/planner"
)

// PlannerDeps holds the dependencies needed to construct a Planner.
type PlannerDeps struct {
	AppName  string
	StateDir string
}

// PlannerSpec describes a planner to be constructed.
type PlannerSpec struct {
	Type   string
	Name   string
	Config map[string]any
}

// PlannerFactory is a function that creates a Planner from deps and spec.
type PlannerFactory func(deps PlannerDeps, spec PlannerSpec) (planner.Planner, error)

var (
	plannerFactories   = make(map[string]PlannerFactory)
	plannerFactoriesMu sync.RWMutex
)

// RegisterPlanner registers a planner factory for the given type name.
// It panics if a factory is already registered for the same name.
func RegisterPlanner(typeName string, factory PlannerFactory) {
	plannerFactoriesMu.Lock()
	defer plannerFactoriesMu.Unlock()
	if _, exists := plannerFactories[typeName]; exists {
		panic("planner already registered: " + typeName)
	}
	plannerFactories[typeName] = factory
}

// LookupPlanner returns the registered planner factory for the given type,
// or nil if not found.
func LookupPlanner(typeName string) (PlannerFactory, bool) {
	plannerFactoriesMu.RLock()
	defer plannerFactoriesMu.RUnlock()
	f, ok := plannerFactories[typeName]
	return f, ok
}