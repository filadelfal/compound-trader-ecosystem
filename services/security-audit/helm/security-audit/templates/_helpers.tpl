{{- define "security-audit.fullname" -}}
{{- printf "%s-security-audit" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
