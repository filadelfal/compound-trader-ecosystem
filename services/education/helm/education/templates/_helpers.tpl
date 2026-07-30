{{- define "education.fullname" -}}
{{- printf "%s-education" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
