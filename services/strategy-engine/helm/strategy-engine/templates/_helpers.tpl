{{- define "strategy-engine.fullname" -}}
{{- printf "%s-strategy-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
