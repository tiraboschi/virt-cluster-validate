#!/bin/sh
#
# Copyright (C) 2024 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Script to check and optionally update copyright/license headers
# Based on kronosnet's update-copyright.sh

set -e

UPDATE_MODE=0

# Parse command line arguments
while [ $# -gt 0 ]; do
    case "$1" in
        --update)
            UPDATE_MODE=1
            shift
            ;;
        *)
            echo "Usage: $0 [--update]"
            echo "  --update  Update copyright dates based on git history"
            echo "  (default: check-only mode)"
            exit 1
            ;;
    esac
done

enddate=$(date +%Y)

# Get all tracked source files that should have copyright headers
# Exclude: vendored third-party code, generated CRDs, binary files (images,
# archives), data files (json), Go module metadata, license files, gitignore
# files, documentation, and OWNERS files.
get_source_files() {
    git ls-files | grep -v -E '^(vendor/|config/crd/bases/|config/rbac/role\.yaml$|go\.mod$|LICENSE$|\.gitignore$|\.dockerignore$|OWNERS$|OWNERS_ALIASES$)' | grep -v -E '\.(png|jpg|jpeg|gif|svg|ico|pdf|tar|gz|zip|json|jsonl|lock|sum|md)$' || true
}

# Update copyright dates for files that already have headers
if [ $UPDATE_MODE -eq 1 ]; then
    echo "Updating copyright dates based on git history..."
    input=$(get_source_files | xargs grep -l "Copyright.*Red Hat" 2>/dev/null || true)

    for i in $input; do
        # Skip updating this script itself to avoid breaking sed commands
        [ "$i" = "hack/check-license-headers.sh" ] && continue
        # Get first commit year from git history
        startdate=$(git log --follow "$i" 2>/dev/null | grep ^Date: | tail -n 1 | awk '{print $6}' || echo "$enddate")

        if [ "$startdate" != "$enddate" ]; then
            # Update to date range
            sed -i -e 's#[Cc]opyright ([Cc]).*Red Hat#Copyright (C) '$startdate'-'$enddate' Red Hat#g' "$i"
        else
            # Single year
            sed -i -e 's#[Cc]opyright ([Cc]).*Red Hat#Copyright (C) '$startdate' Red Hat#g' "$i"
        fi
    done
    echo "Copyright dates updated successfully"
fi

# Check for files missing copyright
echo "Checking for files missing copyright information..."
missing_copyright=0
for i in $(get_source_files); do
    if [ -z "$(grep -i "Copyright" "$i" 2>/dev/null || true)" ]; then
        echo "ERROR: $i is missing Copyright information"
        missing_copyright=$((missing_copyright + 1))
    fi
done

# Check for files missing license
echo "Checking for files missing license information..."
missing_license=0
for i in $(get_source_files); do
    if [ -z "$(grep -i "Apache License" "$i" 2>/dev/null || true)" ]; then
        echo "ERROR: $i is missing Apache License information"
        missing_license=$((missing_license + 1))
    fi
done

# Summary
total_issues=$((missing_copyright + missing_license))
if [ $total_issues -gt 0 ]; then
    echo ""
    echo "License header validation FAILED:"
    echo "  $missing_copyright files missing Copyright"
    echo "  $missing_license files missing Apache License"
    exit 1
fi

echo "All files have proper Copyright and Apache License headers"
exit 0
