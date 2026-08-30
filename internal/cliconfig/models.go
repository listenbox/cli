package cliopenapi

type CLIConfig struct {
	ApiOrigin       string `json:"api_origin"`
	DashboardOrigin string `json:"dashboard_origin"`
	PrintTraceIds   bool   `json:"print_trace_ids"`
}
