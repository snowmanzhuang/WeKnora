package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeDifferentialSubagentConcurrency(t *testing.T) {
	require.Equal(t, 3, NormalizeDifferentialSubagentConcurrency(0))
	require.Equal(t, 1, NormalizeDifferentialSubagentConcurrency(1))
	require.Equal(t, 5, NormalizeDifferentialSubagentConcurrency(5))
	require.Equal(t, 5, NormalizeDifferentialSubagentConcurrency(99))
}
