CREATE TABLE gerrit_users (
	gerrit_username text NOT NULL,
	chat_id text NOT NULL,
	last_report timestamp,
	PRIMARY KEY ( gerrit_username )
);

CREATE INDEX last_report_idx ON gerrit_users ( last_report );

CREATE TABLE inline_comments (
	comment_id text NOT NULL,
	updated_at timestamp NOT NULL,
	PRIMARY KEY ( comment_id )
);

CREATE INDEX updated_at_idx ON inline_comments ( updated_at );

CREATE TABLE team_configs (
	config_key text NOT NULL,
	config_value text NOT NULL,
	PRIMARY KEY ( config_key )
);
