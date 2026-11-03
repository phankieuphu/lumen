# .github

| Path | Purpose |
| --- | --- |
| `pull_request_template.md` | PR checklist (security, performance, housekeeping) |
| `ISSUE_TEMPLATE/` | Bug, feature, and plan-task forms |
| `CODEOWNERS` | Required reviewers for sensitive packages |
| `workflows/ci.yml` | Lint, unit, integration, vuln scan, build matrix |
| `workflows/pr-title.yml` | Conventional Commit check on PR titles |
| `rulesets/main.json` | Branch protection for `main` |
| `labels.yml` | Label set |
| `dependabot.yml` | Weekly Go and Actions updates |

## Apply the ruleset and labels (once, after pushing)

```sh
gh api -X POST repos/{owner}/{repo}/rulesets --input .github/rulesets/main.json

yq -o=json '.[]' .github/labels.yml | jq -c . | while read -r l; do
  gh label create --force "$(jq -r .name <<<"$l")" \
    --color "$(jq -r .color <<<"$l")" --description "$(jq -r .description <<<"$l")"
done
```

`required_signatures` needs signed commits (SSH or GPG). Remove that rule if
you don't sign yet.
