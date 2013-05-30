#!/bin/bash
#
# Starts the telemetry_slave_scripts/vm_capture_archives.sh script on all
# slaves.
#
# The script should be run from the skia-telemetry-master GCE instance's
# /home/default/skia-repo/buildbot/compute_engine_scripts/telemetry/telemetry_master_scripts
# directory.
#
# Copyright 2013 Google Inc. All Rights Reserved.
# Author: rmistry@google.com (Ravi Mistry)

source ../vm_config.sh

# Update buildbot.
gclient sync

NUM_PAGESETS=$(($NUM_WEBPAGES/$NUM_SLAVES))
START=1
for SLAVE_NUM in $(seq 1 $NUM_SLAVES); do
  END=$(expr $START + $NUM_PAGESETS - 1)
  CMD="bash vm_capture_archives.sh $SLAVE_NUM alexa$START-$END.json"
  # Still trying to figure the below now.
  ssh -f -X -o UserKnownHostsFile=/dev/null -o CheckHostIP=no \
    -o StrictHostKeyChecking=no -i /home/default/.ssh/google_compute_engine \
    -A -p 22 default@108.170.222.$SLAVE_NUM -- "source .bashrc; cd skia-repo/buildbot/compute_engine_scripts/telemetry/telemetry_slave_scripts; $CMD > /tmp/capture_archives_output.txt 2>&1"
  START=$(expr $END + 1)
done
