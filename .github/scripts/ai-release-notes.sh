#!/bin/bash

previous_tag="${1}"

# Get merge commits only
if [ "$previous_tag" ]; then
    merge_commits=$(git log --oneline --merges --first-parent --no-decorate $previous_tag..HEAD)
else 
    merge_commits=$(git log --oneline --merges --first-parent --no-decorate)
fi

# Extract PR numbers from merge commits
pr_numbers=$(echo "$merge_commits" | grep -oP '(?<=Merge pull request #)\d+' | sort -u)

# Convert PR numbers to array
pr_array=($pr_numbers)

# Fetch PR details from GitHub API using GraphQL for batching
pr_details=""
if [ ${#pr_array[@]} -gt 0 ]; then
    # Split repository into owner and name
    IFS='/' read -r repo_owner repo_name <<< "$GITHUB_REPOSITORY"
    
    # Process PRs in batches of 50 (GraphQL limit)
    batch_size=50
    for ((i=0; i<${#pr_array[@]}; i+=batch_size)); do
        # Build GraphQL query for batch
        graphql_query="query {"
        batch_end=$((i + batch_size))
        if [ $batch_end -gt ${#pr_array[@]} ]; then
            batch_end=${#pr_array[@]}
        fi
        
        for ((j=i; j<batch_end; j++)); do
            pr_num=${pr_array[j]}
            graphql_query="${graphql_query} pr${pr_num}: repository(owner: \"${repo_owner}\", name: \"${repo_name}\") { pullRequest(number: ${pr_num}) { number title body author { login } } }"
        done
        graphql_query="${graphql_query} }"
        
        # Execute GraphQL query
        graphql_response=$(curl -s -X POST \
            -H "Authorization: bearer $GITHUB_TOKEN" \
            -H "Content-Type: application/json" \
            -d "{\"query\": \"$(echo $graphql_query | sed 's/"/\\"/g')\"}" \
            "https://api.github.com/graphql")
        
        # Parse response
        if [ -f ./pr-details.txt ]; then
            rm ./pr-details.txt
        fi
        for ((j=i; j<batch_end; j++)); do
            pr_num=${pr_array[j]}
            pr_data=$(echo "$graphql_response" | jq -r ".data.pr${pr_num}.pullRequest")
            
            if [ "$pr_data" != "null" ]; then
                pr_title=$(echo "$pr_data" | jq -r '.title // empty')
                pr_body=$(echo "$pr_data" | jq -r '.body // empty' | head -n 20)
                pr_user=$(echo "$pr_data" | jq -r '.author.login // "unknown"')
                
                if [ -n "$pr_title" ]; then
                    echo "PR #${pr_num}: ${pr_title} (by @${pr_user})" >> ./pr-details.txt
                    if [ -n "$pr_body" ] && [ "$pr_body" != "null" ]; then
                        # Remove HTML tags and limit to 512 characters
                        pr_body_clean=$(echo "$pr_body" | sed -e 's/<[^>]*>//g' -e 's/&nbsp;/ /g' -e 's/&quot;/"/g' -e 's/&amp;/\&/g' -e 's/&lt;/</g' -e 's/&gt;/>/g' | tr '\n' ' ' | sed 's/  */ /g' | cut -c1-512)
                        if [ -n "$pr_body_clean" ]; then
                            echo "  ${pr_body_clean}..." >> ./pr-details.txt
                        fi
                    fi
                    echo "" >> ./pr-details.txt
                fi
            fi
        done
    done
fi

# Create a minified changelog combining merge commits and PR details
minified_changelog=$(cat <<EOF
Merged Pull Requests:
${merge_commits}

Pull Request Details:
$(cat ./pr-details.txt)
EOF
)
rm ./pr-details.txt

# Sanitize the changelog by escaping backticks to prevent command execution
changelog_safe="${minified_changelog//\`/\\\`}"

# Fetch previous release notes in a more readable format, removing everything after <details>
# Only include non-prerelease and non-draft versions, limit to 5 releases
previous_releases=""
release_count=0
while IFS= read -r release; do
    if [ $release_count -ge 5 ]; then
        break
    fi
    name=$(echo "$release" | jq -r '.name')
    body=$(echo "$release" | jq -r '.body' | sed '/<details>/,$d' | sed 's/[[:space:]]*$//')
    if [ -n "$body" ]; then
        previous_releases="${previous_releases}$(echo -e "\nRelease: ${name}\n${body}\n---\n")"
        ((release_count++))
    fi
done < <(curl -s https://api.github.com/repos/$GITHUB_REPOSITORY/releases | jq -c '.[] | select(.prerelease == false and .draft == false)')
previous_release_notes=$(echo -e "Recent releases for reference:\n${previous_releases}")

conv=$(cat <<EOF
You are a helpful assistant that generates release notes for a software project.
You will be given a changelog with merged pull requests and their details.

$changelog_safe

Based on the PR titles and descriptions above, generate concise release notes highlighting the most important changes.
Focus on user-facing features, bug fixes, and breaking changes.

The release notes will be embedded in the following format:
### Major Changes
{{ AI RESPONSE }}

<details>
...
</details>

Guidelines:
- Keep it short and concise, maximum 5-6 bullet points
- Prioritize user-facing changes and important bug fixes
- Group similar changes together when possible
- Use clear, non-technical language when possible
- Use markdown formatting

Do NOT respond with suggestions! The response will be processed programmatically and embedded in the release notes.
Do NOT include the details tag or content in the response. Do NOT include the Major Changes header in the response.

Previous release notes for context:
$previous_release_notes
EOF
)

#echo "$conv"
#exit 0

conv_json=$(echo "$conv" | jq -R -s '{"model": "'"$OPENROUTER_MODEL"'", "messages": [{"role": "user", "content": .}]}')
conv_response=$(curl -X POST -H "Authorization: Bearer $OPENROUTER_TOKEN" -H "Content-Type: application/json" -d "$conv_json" https://openrouter.ai/api/v1/chat/completions)
echo "$conv_response"

conv_response_text=$(echo "$conv_response" | jq -r '.choices[0].message.content')

echo "release_notes<<EOF" >> $GITHUB_OUTPUT
echo "$conv_response_text" >> $GITHUB_OUTPUT
echo "EOF" >> $GITHUB_OUTPUT
