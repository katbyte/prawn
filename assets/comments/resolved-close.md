{{ if .Landed }}It appears the change proposed here has since landed in [{{ .LandedRef }}]({{ .LandedURL }}){{ if .Shipped }} (shipped in {{ .Shipped }}){{ end }}, so as part of a cleanup of historical pull requests we are closing this one out as already delivered.

If there is still something here that change missed, please rebase onto the current codebase and open a fresh PR referencing this one and we will take another look. Thank you for the contribution!{{ else if .Superseded }}It appears the change proposed here has been superseded — [as noted in the thread]({{ .URL }}) it has since been covered elsewhere, so as part of a cleanup of historical pull requests we are closing this one out.

If there is still something here the other change missed, please rebase onto the current codebase and open a fresh PR referencing this one and we will take another look. Thank you for the contribution!{{ else }}The issue{{ if .Plural }}s{{ end }} this PR addresses ({{ .Issues }}) {{ if .Plural }}have{{ else }}has{{ end }} since been closed, so as part of a cleanup of historical pull requests we are closing it as no longer required.

If this change is still relevant on the current codebase, please rebase and open a fresh PR referencing this one and we will take another look. Thank you for the contribution!{{ end }}
