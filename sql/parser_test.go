// Licensed to LinDB under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. LinDB licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package sql

import (
	"fmt"
	"testing"

	"github.com/antlr/antlr4/runtime/Go/antlr"
	"github.com/stretchr/testify/assert"

	"github.com/lindb/lindb/pkg/encoding"
	"github.com/lindb/lindb/sql/grammar"
	"github.com/lindb/lindb/sql/stmt"
)

func Test_SQL_Parse(t *testing.T) {
	query, err := Parse("select f+100 from cpu")
	assert.NoError(t, err)
	data := encoding.JSONMarshal(&query)
	query1 := stmt.Query{}
	err = encoding.JSONUnmarshal(data, &query1)
	assert.NoError(t, err)
}

func Test_Sql_examples(t *testing.T) {
	examples := []struct {
		ok  bool
		sql string
	}{
		{true, "select x from y where name = 'sss'"},
		{true, "select x from y where update = 'sss'"},
		{true, "select x from y where metric = 'sss' and drop='xxx'"},
		{false, "drop x from y where drop = 'sss'"},
	}
	for _, example := range examples {
		_, err := Parse(example.sql)
		if example.ok {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	}
}

func Test_Meta_SQL_Parse(t *testing.T) {
	query, err := Parse("show databases")
	assert.NoError(t, err)
	_, ok := query.(*stmt.Metadata)
	assert.True(t, ok)
}

func TestParse_panic(t *testing.T) {
	defer func() {
		getSQLParserFunc = getSQLParser
	}()
	getSQLParserFunc = func(tokenStream *antlr.CommonTokenStream) *grammar.SQLParser {
		panic(fmt.Errorf("err"))
	}
	_, err := Parse("select f+100 from cpu")
	assert.Error(t, err)

	getSQLParserFunc = func(tokenStream *antlr.CommonTokenStream) *grammar.SQLParser {
		panic(123)
	}
	_, err = Parse("select f+100 from cpu")
	assert.Error(t, err)
}

func BenchmarkSQLParse(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = Parse("select f from cpu " +
			"where (ip in ('1.1.1.1','2.2.2.2')" +
			" and region='sh') and (path='/data' or path='/home')")
	}
}
