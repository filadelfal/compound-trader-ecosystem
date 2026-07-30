{{- define "cloud-infra.fullname" -}}
{{- printf "%s-cloud-infra" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
