package pdf

import "testing"

func TestCatalogContainsEveryRequiredKeyInBothLocales(t *testing.T) {
	for _, locale := range []string{"en", "uk"} {
		catalog, err := NewCatalog(locale)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range RequiredKeys() {
			if catalog.T(key) == "" {
				t.Errorf("locale %s is missing %s", locale, key)
			}
		}
		for value := range baseEnums {
			if catalog.Enum(value) == "" || catalog.Enum(value) == value {
				t.Errorf("locale %s has no translation for enum %s", locale, value)
			}
		}
	}
}

func TestCatalogRejectsUnsupportedLocale(t *testing.T) {
	if _, err := NewCatalog("de"); err == nil {
		t.Fatal("NewCatalog accepted unsupported locale")
	}
}
