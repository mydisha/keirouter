// The time ranges every analytics page offers, in one place so Usage, Quota
// and Overview never drift apart again. Values are the backend's period
// tokens (see sinceForPeriod in gateway/insights.go).
export const REPORT_PERIODS = [
  { value: "today", label: "Today" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "90d", label: "90d" },
] as const;

export type ReportPeriod = (typeof REPORT_PERIODS)[number]["value"];
