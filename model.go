package plugins

// IModel ORM 模型接口
// 所有需要在 ORM 中使用的模型类型都需要实现此接口
type IModel interface {
	// TableName 返回模型对应的表名
	TableName() string
}
