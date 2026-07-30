{{- define "propfirm-mode.fullname" -}}
{{- printf "%s-propfirm-mode" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
