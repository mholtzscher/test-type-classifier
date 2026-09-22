package shop

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestCreateOrder(t *testing.T) {
	t.Parallel()
	require.NoError(t, nil)

	t.Run("database_failure", func(t *testing.T) {
		db, err := sql.Open("postgres", "dsn")
		_ = db
		_ = err
	})

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.got)
		})
	}
}

func TestGoldenOutput(t *testing.T) {
	assertGolden(t, "out.txt")
}

func FuzzParseName(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, input string) {})
}

func BenchmarkParseName(b *testing.B) {
	for i := 0; i < b.N; i++ {
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func helper(t *testing.T) {}
