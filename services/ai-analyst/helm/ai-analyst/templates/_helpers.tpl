{{- define "ai-analyst.fullname" -}}
{{- printf "%s-ai-analyst" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
