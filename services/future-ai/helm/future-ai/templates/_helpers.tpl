{{- define "future-ai.fullname" -}}
{{- printf "%s-future-ai" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
