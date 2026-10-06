package httpfixture

import (
	"testing"

	"github.com/pooya79/Piko/internal/locale"
)

func TestLocaleCatalog(t *testing.T) *locale.Catalog {
	t.Helper()
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
