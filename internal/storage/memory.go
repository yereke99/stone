package storage

import (
	"context"
	"sync"

	"github.com/yereke99/stone/internal/bot"
)

type MemoryStore struct {
	mu     sync.RWMutex
	states map[string]bot.State
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		states: make(map[string]bot.State),
	}
}

func (s *MemoryStore) Get(ctx context.Context, userID string) (bot.State, bool, error) {
	if err := ctx.Err(); err != nil {
		return bot.State{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.states[userID]
	return state, ok, nil
}

func (s *MemoryStore) Save(ctx context.Context, state bot.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.states[state.UserID] = state
	return nil
}
