{{- define "analytics-engine.fullname" -}}
{{- printf "%s-analytics-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
