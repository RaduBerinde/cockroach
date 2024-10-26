#!/bin/bash

function usage() {
  echo "Usage: $0 <repo-directory> <output-patch-file>"
  echo "This will create a patch file from the current git diff in the repository directory."
  echo ""
  echo "Example: $0 ~/pebble com_github_cockroachdb_pebble.patch"
  exit 1
}

# Check for required arguments
if [ "$#" -ne 2 ]; then
  usage
fi

REPO_DIR="$1"
OUTPUT_PATCH_FILE="$2"

# Check if the repository directory exists
if [ ! -d "$REPO_DIR" ]; then
  echo "Error: Repository directory '$REPO_DIR' does not exist."
  exit 1
fi

# Check if the directory is a git repository
if [ ! -d "$REPO_DIR/.git" ]; then
  echo "Error: '$REPO_DIR' is not a Git repository."
  exit 1
fi

# Create the patch file from git diff
echo "Creating patch $OUTPUT_PATCH_FILE from the current git diff in $REPO_DIR"
echo ""
echo "Note: This diff contains staged changes and unstaged changes to tracked"
echo "      files.  It does not include untracked, unstaged files."
echo ""

cd "$REPO_DIR" || exit 1
if git diff --no-ext-diff HEAD > "$OUTPUT_PATCH_FILE"; then
  echo "Patch file created successfully at '$OUTPUT_PATCH_FILE'."
else
  echo "Failed to create patch file."
  exit 1
fi

