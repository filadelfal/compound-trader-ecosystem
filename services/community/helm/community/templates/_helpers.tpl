{{- define "community.fullname" -}}
{{- printf "%s-community" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
