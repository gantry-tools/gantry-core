package cluster

type CLICommand struct {
	Path          string `json:"path"`
	Mutation      bool   `json:"mutation"`
	SecretBearing bool   `json:"secret_bearing"`
}

var CommonCLI = []CLICommand{
	{Path: "cluster init", Mutation: true},
	{Path: "cluster invite", Mutation: true, SecretBearing: true},
	{Path: "cluster join", Mutation: true, SecretBearing: true},
	{Path: "cluster approve", Mutation: true, SecretBearing: true},
	{Path: "cluster members"},
	{Path: "cluster status"},
	{Path: "cluster rotate", Mutation: true, SecretBearing: true},
	{Path: "cluster revoke", Mutation: true},
	{Path: "cluster remove", Mutation: true},
}
