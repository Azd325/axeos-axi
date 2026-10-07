package app

// Source: bitaxeorg/ESP-Miner tag v2.15.3, GET /api/system/info
// (main/http_server/system_api_json.c and main/http_server/http_server.c).
var conditionalInfoFields = []string{
	"power_fault", "hardware_fault",
	"blockHeight", "scriptsig", "networkDifficulty", "coinbaseValueTotalSatoshis",
	"coinbaseValueUserSatoshis", "blockSignals", "coinbaseOutputs",
	"hashrateMonitor", "mdnsHostname",
}

const conditionalFieldsHelp = "fields the firmware sends only on a condition print null when absent"

func readsInfoFields(command string) bool {
	return command == "" || command == "info" || command == "asic"
}
