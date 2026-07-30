{{- define "journal-engine.fullname" -}}
{{- printf "%s-journal-engine" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
