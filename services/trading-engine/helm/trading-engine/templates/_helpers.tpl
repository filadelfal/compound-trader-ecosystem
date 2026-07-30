{{- define "trading-engine.fullname" -}}
{{- printf "%s-trading-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
