#!/bin/bash

# Sanitize the changelog by escaping backticks to prevent command execution
changelog_safe="${1//\`/\\\`}"

previous_release_notes=$(curl -s https://api.github.com/repos/$GITHUB_REPOSITORY/releases | jq -c '.[0:8][] | {"body": .body, "name": .name}')

conv=$(cat <<EOF
You are a helpful assistant that generates release notes for a software project.
You will be given a changelog and you will need to generate release notes for the following changelog:
$changelog_safe

The release notes will be embedded in the following format:
### Major Changes
{{ AI RESPONSE }}

<details>
...
</details>

Avoid long release notes. Keep it short and concise, a few bullet points (max 5-6). Use markdown formatting if needed.
Do NOT respond with suggestions! The response will be processed programmatically and embedded in the release notes.
Do NOT include the details tag or content in the response. Do NOT include the Major Changes header in the response.

Previous release notes:
$previous_release_notes
EOF
)

conv_json=$(echo "$conv" | jq -R -s '{"model": "'"$OPENROUTER_MODEL"'", "messages": [{"role": "user", "content": .}]}')
conv_response=$(curl -X POST -H "Authorization: Bearer $OPENROUTER_TOKEN" -H "Content-Type: application/json" -d "$conv_json" https://openrouter.ai/api/v1/chat/completions)
echo "$conv_response"

conv_response_text=$(echo "$conv_response" | jq -r '.choices[0].message.content')

echo "release_notes<<EOF" >> $GITHUB_OUTPUT
echo "$conv_response_text" >> $GITHUB_OUTPUT
echo "EOF" >> $GITHUB_OUTPUT
