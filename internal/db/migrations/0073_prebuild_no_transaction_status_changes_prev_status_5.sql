-- Prebuild step 5: vacuum analyze the converted table after the cluster.

-- After the cluster, to reset the visibility map and refresh correlation stats.
vacuum analyze status_changes_new;
