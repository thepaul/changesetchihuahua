CREATE TABLE gerrit_users (
	gerrit_username text NOT NULL,
	gerrit_email text,
	slack_id text,
	PRIMARY KEY ( gerrit_username )
);
