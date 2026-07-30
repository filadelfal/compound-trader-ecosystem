{{- define "market-data.fullname" -}}
{{- printf "%s-market-data" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
