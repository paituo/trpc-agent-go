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
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/planner/a2ui"
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
)

const (
	plannerTypeBuiltin = "builtin"
	plannerTypeReact   = "react"
	plannerTypeA2UI    = "a2ui"
)

func init() {
	RegisterPlanner(plannerTypeBuiltin, newBuiltinPlanner)
	RegisterPlanner(plannerTypeReact, newReactPlanner)
	RegisterPlanner(plannerTypeA2UI, newA2UIPlanner)
}

func newBuiltinPlanner(
	_ PlannerDeps,
	spec PlannerSpec,
) (planner.Planner, error) {
	var opts builtin.Options

	if spec.Config != nil {
		cfg, err := parseBuiltinPlannerConfig(spec.Config)
		if err != nil {
			return nil, fmt.Errorf("parse builtin planner config: %w", err)
		}
		opts = cfg
	}

	return builtin.New(opts), nil
}

func newReactPlanner(
	_ PlannerDeps,
	_ PlannerSpec,
) (planner.Planner, error) {
	return react.New(), nil
}

func newA2UIPlanner(
	_ PlannerDeps,
	spec PlannerSpec,
) (planner.Planner, error) {
	var opts []a2ui.Option

	if spec.Config != nil {
		cfg, err := parseA2UIPlannerConfig(spec.Config)
		if err != nil {
			return nil, fmt.Errorf("parse a2ui planner config: %w", err)
		}
		opts = cfg
	}

	return a2ui.New(opts...), nil
}

func parseBuiltinPlannerConfig(cfg map[string]any) (builtin.Options, error) {
	var opts builtin.Options

	if v, ok := cfg["reasoning_effort"].(string); ok && v != "" {
		opts.ReasoningEffort = &v
	}
	if v, ok := cfg["thinking_enabled"].(bool); ok {
		opts.ThinkingEnabled = &v
	}
	if v, ok := cfg["thinking_tokens"].(float64); ok {
		tokens := int(v)
		opts.ThinkingTokens = &tokens
	}

	return opts, nil
}

func parseA2UIPlannerConfig(cfg map[string]any) ([]a2ui.Option, error) {
	var opts []a2ui.Option

	if v, ok := cfg["instruction"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithInstruction(v))
	}
	if v, ok := cfg["server_to_client_with_standard_catalog"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithServerToClientWithStandardCatalogSchema(v))
	}
	if v, ok := cfg["client_to_server"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithClientToServerSchema(v))
	}
	if v, ok := cfg["client_capabilities_schema"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithClientCapabilitiesSchema(v))
	}
	if v, ok := cfg["server_to_client"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithServerToClientSchema(v))
	}
	if v, ok := cfg["standard_catalog_definition"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithStandardCatalogDefinition(v))
	}
	if v, ok := cfg["catalog_description_schema"].(string); ok && v != "" {
		opts = append(opts, a2ui.WithCatalogDescriptionSchema(v))
	}

	return opts, nil
}

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