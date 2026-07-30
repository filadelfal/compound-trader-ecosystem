{{- define "api-gateway.fullname" -}}
{{- printf "%s-api-gateway" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
