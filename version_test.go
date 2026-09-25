package autotel_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jagreehal/autotel-go/v2"
)

func TestVersion(t *testing.T) {
	require.Equal(t, "2.3.0", autotel.GetVersion())
}
