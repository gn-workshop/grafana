package accesscontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/grafana/grafana/pkg/infra/localcache"
	"github.com/grafana/grafana/pkg/infra/log"
)

// ScopeAttributeResolver is used to resolve attributes in scopes to one or more scopes that are
// evaluated by logical or. E.g. "dashboards:id:1" -> "dashboards:uid:test-dashboard" or "folder:uid:test-folder"
type ScopeAttributeResolver interface {
	Resolve(ctx context.Context, orgID int64, scope string) ([]string, error)
}

type ActionResolver interface {
	ResolveAction(action string) []string
	ResolveActionSet(actionSet string) []string
	ExpandActionSets(permissions []Permission) []Permission
}

// ScopeAttributeResolverFunc is an adapter to allow functions to implement ScopeAttributeResolver interface
type ScopeAttributeResolverFunc func(ctx context.Context, orgID int64, scope string) ([]string, error)

func (f ScopeAttributeResolverFunc) Resolve(ctx context.Context, orgID int64, scope string) ([]string, error) {
	return f(ctx, orgID, scope)
}

type ScopeAttributeMutator func(context.Context, string) ([]string, error)

type ActionSetResolver func(context.Context, string) []string

const (
	ttl           = 30 * time.Second
	cleanInterval = 2 * time.Minute
)

func NewResolvers(log log.Logger) Resolvers {
	return Resolvers{
		log:                log,
		cache:              localcache.New(ttl, cleanInterval),
		attributeResolvers: map[string]ScopeAttributeResolver{},
	}
}

type Resolvers struct {
	log                log.Logger
	cache              *localcache.CacheService
	attributeResolvers map[string]ScopeAttributeResolver
	actionResolver     ActionResolver
}

func (s *Resolvers) AddScopeAttributeResolver(prefix string, resolver ScopeAttributeResolver) {
	s.log.Debug("Adding scope attribute resolver", "prefix", prefix)
	s.attributeResolvers[prefix] = resolver
}

func (s *Resolvers) SetActionResolver(resolver ActionResolver) {
	s.actionResolver = resolver
}

func (s *Resolvers) GetScopeAttributeMutator(orgID int64) ScopeAttributeMutator {
	return func(ctx context.Context, scope string) ([]string, error) {
		key := getScopeCacheKey(orgID, scope)
		// Check cache before computing the scope
		if cachedScope, ok := s.cache.Get(key); ok {
			scopes := cachedScope.([]string)
			s.log.Debug("Used cache to resolve scope", "scope", scope, "resolved_scopes", scopes)
			return scopes, nil
		}

		prefix := ScopePrefix(scope)
		if resolver, ok := s.attributeResolvers[prefix]; ok {
			scopes, err := resolver.Resolve(ctx, orgID, scope)
			if err != nil {
				return nil, fmt.Errorf("could not resolve %v: %w", scope, err)
			}
			// Cache result
			s.cache.Set(key, scopes, ttl)
			s.log.Debug("Resolved scope", "scope", scope, "resolved_scopes", scopes)
			return scopes, nil
		}
		return nil, ErrResolverNotFound
	}
}

// getScopeCacheKey creates an identifier to fetch and store resolution of scopes in the cache
func getScopeCacheKey(orgID int64, scope string) string {
	return fmt.Sprintf("%s-%v", scope, orgID)
}

func (s *Resolvers) GetActionSetResolver() ActionSetResolver {
	return func(ctx context.Context, action string) []string {
		if s.actionResolver == nil {
			return []string{action}
		}
		actionSetActions := s.actionResolver.ResolveAction(action)
		actions := append(actionSetActions, action)
		s.log.Debug("Resolved action", "action", action, "resolved_actions", actions)
		return actions
	}
}
