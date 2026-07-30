{{- define "user-management.fullname" -}}
{{- printf "%s-user-management" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
