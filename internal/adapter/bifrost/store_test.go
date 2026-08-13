package bifrost

import "testing"

func TestConfiguredLogWriterPolicyIsPassedToBifrost(t *testing.T) {
	writer := logWriterConfig(StoreConfig{
		WriterMaxBatchSize:  17,
		WriterBatchInterval: "275ms",
		WriterQueueCapacity: 93,
	})
	if writer.MaxBatchSize != 17 || writer.BatchInterval != "275ms" || writer.WriteQueueCapacity != 93 {
		t.Fatalf("Bifrost Log Store writer config = %+v", writer)
	}
}
