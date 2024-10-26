#!/bin/bash

function usage() {
  echo "This script runs gazelle and applies a patch to a repository clone."
  echo ""
  echo "Usage: $0 <input-patch-file> <repo-directory>"
  echo ""
  echo "Example: $0 com_github_cockroachdb_pebble.patch ~/pebble"
  exit 1
}

# Check for required arguments
if [ "$#" -ne 2 ]; then
  usage
fi

PATCH_FILE="$1"
REPO_DIR="$2"

## Check if the patch file exists
#if [ ! -f "$PATCH_FILE" ]; then
#  echo "Error: Patch file '$PATCH_FILE' does not exist."
#  exit 1
#fi

# Check if the filename ends in ".patch"
if ! [[ "$PATCH_FILE" == *.patch ]]; then
  echo "Filename '$PATCH_FILE' does not end in '.patch'"
  exit 1
fi
# Strip the ".patch" suffix
BASENAME="${PATCH_FILE%.patch}"
echo $BASENAME

# Generate the import path from the basename. For example,
# com_github_cockroachdb_pebble.patch to github.com/cockroachdb/pebble.

# Split the input string by underscores
IFS='_' read -r -a fields <<< "$BASENAME"

# Extract the first two fields
field1="${fields[0]}"
field2="${fields[1]}"

# Join the remaining fields with '/'
rest=$(IFS=/; echo "${fields[*]:2}")

IMPORT_PATH="${field2}.${field1}/${rest}"

echo "Import path: $IMPORT_PATH"

# Check if the repository directory exists
if [ ! -d "$REPO_DIR" ]; then
  echo "Error: Repository directory '$REPO_DIR' does not exist."
  exit 1
fi

cd "$REPO_DIR" || exit 1

#if [ ! -f WORKSPACE ]; then
#	echo "Generating WORKSPACE"
#	echo 'workspace(name = "com_github_cockroachdb_pebble")' > WORKSPACE
#fi

# Apply the patch
echo "Applying patch $PATCH_FILE to directory $REPO_DIR"
echo ""

if patch -p1 < "$PATCH_FILE"; then
  echo "Patch applied successfully."
else
  echo "Failed to apply patch."
  echo "Make sure the repository is at the proper commit."
  exit 1
fi
