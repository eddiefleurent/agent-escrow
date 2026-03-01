-- Migration 024: add advisory human/AI delegate preference to decomposition nodes.
ALTER TABLE decomposition_nodes
ADD COLUMN delegate_preference TEXT NOT NULL DEFAULT ''
    CHECK(delegate_preference IN ('', 'human', 'ai', 'any'));
