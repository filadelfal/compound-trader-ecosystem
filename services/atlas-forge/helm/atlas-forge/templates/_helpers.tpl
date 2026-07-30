{{- define "atlas-forge.fullname" -}}
{{- printf "%s-atlas-forge" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
