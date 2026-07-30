{{- define "ai-coach.fullname" -}}
{{- printf "%s-ai-coach" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
