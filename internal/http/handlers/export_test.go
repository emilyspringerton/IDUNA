package handlers

// SetLiveTrimThresholdsForTest lets the external test package (internal/http/handlers_test)
// shrink the live-tracker's line-buffer trim thresholds for one test, so a real-but-modest-sized
// fixture can exercise the trim path without needing an actual 120,000-line capture. Returns a
// restore func the caller must defer.
func SetLiveTrimThresholdsForTest(max, head int) (restore func()) {
	oldMax, oldHead := hsLiveMaxPower, hsLiveHeadPower
	hsLiveMaxPower, hsLiveHeadPower = max, head
	return func() { hsLiveMaxPower, hsLiveHeadPower = oldMax, oldHead }
}
