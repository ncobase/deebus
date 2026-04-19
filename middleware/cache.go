package middleware

import (
	"fmt"

	"github.com/ncobase/deebus/providers"
)

func cacheProvider(p providers.Provider) (providers.CacheProvider, error) {
	cp, ok := p.(providers.CacheProvider)
	if !ok {
		return nil, fmt.Errorf("provider %q does not support cache management", p.Name())
	}
	return cp, nil
}
