{{- define "backtest-engine.fullname" -}}
{{- printf "%s-backtest-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
