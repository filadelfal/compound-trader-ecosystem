{{- define "portfolio-engine.fullname" -}}
{{- printf "%s-portfolio-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
