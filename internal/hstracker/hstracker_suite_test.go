package hstracker

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHSTrackerSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HSTracker Suite")
}
