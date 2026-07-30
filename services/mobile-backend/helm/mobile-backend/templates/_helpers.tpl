{{- define "mobile-backend.fullname" -}}
{{- printf "%s-mobile-backend" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
