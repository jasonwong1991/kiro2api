package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveRefreshIndicesUnlocked_RequestAllWithProxyRespectsBatchSizeAndActivePool(t *testing.T) {
	tm := &TokenManager{
		configs: []AuthConfig{
			{AuthType: AuthMethodSocial, RefreshToken: "t0"},
			{AuthType: AuthMethodSocial, RefreshToken: "t1"},
			{AuthType: AuthMethodSocial, RefreshToken: "t2"},
			{AuthType: AuthMethodSocial, RefreshToken: "t3", Disabled: true},
		},
		batchSize:  2,
		activePool: []int{3, 1, 2},
		proxyPool:  &ProxyPoolManager{}, // 仅用于标记“代理启用”
	}

	indices := tm.resolveRefreshIndicesUnlocked(nil)
	assert.Equal(t, []int{1, 2}, indices)
}

func TestResolveRefreshIndicesUnlocked_RequestAllWithoutProxyKeepsOriginalBehavior(t *testing.T) {
	tm := &TokenManager{
		configs: []AuthConfig{
			{AuthType: AuthMethodSocial, RefreshToken: "t0"},
			{AuthType: AuthMethodSocial, RefreshToken: "t1", Disabled: true},
			{AuthType: AuthMethodSocial, RefreshToken: "t2"},
		},
		batchSize:  2,
		activePool: []int{2, 0},
		proxyPool:  nil, // 未启用代理
	}

	indices := tm.resolveRefreshIndicesUnlocked(nil)
	assert.Equal(t, []int{0, 2}, indices)
}

func TestResolveRefreshIndicesUnlocked_ExplicitIndicesAreNotCapped(t *testing.T) {
	tm := &TokenManager{
		configs: []AuthConfig{
			{AuthType: AuthMethodSocial, RefreshToken: "t0"},
			{AuthType: AuthMethodSocial, RefreshToken: "t1"},
			{AuthType: AuthMethodSocial, RefreshToken: "t2"},
		},
		batchSize: 2,
		proxyPool: &ProxyPoolManager{},
	}

	indices := tm.resolveRefreshIndicesUnlocked([]int{2, 1, 0})
	assert.Equal(t, []int{2, 1, 0}, indices)
}

