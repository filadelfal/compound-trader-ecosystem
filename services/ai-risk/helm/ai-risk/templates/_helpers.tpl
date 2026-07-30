{{- define "ai-risk.fullname" -}}
{{- printf "%s-ai-risk" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
