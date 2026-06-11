#!/bin/bash

# 获取所有paituo的提交并分析修改统计
git log --all --author="paituo" --pretty=format:"%H" | while read commit; do
    echo "=== Commit: $commit ==="
    git show $commit --stat --pretty=format:"%H|%an|%ae|%ad|%s" --date=short | head -100
    echo ""
done