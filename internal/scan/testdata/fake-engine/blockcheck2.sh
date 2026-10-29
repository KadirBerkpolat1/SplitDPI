#!/bin/sh
MARK_FILE="${MARK_FILE:-$PWD/.int-mark}"
CANCEL_FILE="${MARK_FILE}.cancel"
trap 'touch "$MARK_FILE"; exit 130' INT
echo line1
echo line2
echo line3
while [ ! -f "$CANCEL_FILE" ]; do
	sleep 0.02
done
touch "$MARK_FILE"
exit 130
