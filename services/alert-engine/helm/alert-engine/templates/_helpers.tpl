{{- define "alert-engine.fullname" -}}
{{- printf "%s-alert-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
