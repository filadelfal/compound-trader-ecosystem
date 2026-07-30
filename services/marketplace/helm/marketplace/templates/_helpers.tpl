{{- define "marketplace.fullname" -}}
{{- printf "%s-marketplace" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
