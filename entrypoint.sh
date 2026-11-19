#!/bin/sh

DB_PATH="${S3UI_DB_FOLDER:-/app/db}/3s-ui.db"
if [ -f "$DB_PATH" ]; then
	./3sui migrate
fi

exec ./3sui