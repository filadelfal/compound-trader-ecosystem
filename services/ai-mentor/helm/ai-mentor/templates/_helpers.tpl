{{- define "ai-mentor.fullname" -}}
{{- printf "%s-ai-mentor" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
