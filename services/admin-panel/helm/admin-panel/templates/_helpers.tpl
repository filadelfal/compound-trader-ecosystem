{{- define "admin-panel.fullname" -}}
{{- printf "%s-admin-panel" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
