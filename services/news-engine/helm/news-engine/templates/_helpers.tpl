{{- define "news-engine.fullname" -}}
{{- printf "%s-news-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
