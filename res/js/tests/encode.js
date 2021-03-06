describe('Test sk encoding and decoding functions.',
  function() {
    function testEncode() {
      assert.equal(sk.query.fromObject({}), "");
      assert.equal(sk.query.fromObject({a: 2}), "a=2");
      assert.equal(sk.query.fromObject({a: "2"}), "a=2");
      assert.equal(sk.query.fromObject({a: "2 3"}), "a=2%203");
      assert.equal(sk.query.fromObject({"a b": "2 3"}), "a%20b=2%203");
      assert.equal(sk.query.fromObject({"a": [2, 3]}), "a=2&a=3");
      assert.equal(sk.query.fromObject({"a": ["2", "3"]}), "a=2&a=3");
      assert.equal(sk.query.fromObject({"a": []}), "");
      assert.equal(sk.query.fromObject({"a": {b: "3"}}), "a=b%3D3");
      assert.equal(sk.query.fromObject({"a": {b: "3"}, b: "3"}), "a=b%3D3&b=3");
      assert.equal(sk.query.fromObject({"a": {}, b: "3"}), "a=&b=3");
      assert.equal(sk.query.fromObject({"a": {b: {c: "foo bar"}}}), "a=b%3Dc%253Dfoo%252520bar");
      assert.isTrue(["a=2&b=3", "b=3&a=2"].indexOf(sk.query.fromObject({a: 2, b:3})) != -1);
    }

    function testDecodeToObject() {
      assert.deepEqual(sk.query.toObject("",        {}),         {});
      assert.deepEqual(sk.query.toObject("a=2",     {}),         {a: "2"});
      assert.deepEqual(sk.query.toObject("a=2",     {a: "foo"}), {a: "2"});
      assert.deepEqual(sk.query.toObject("a=2",     {a: 1.0}),   {a: 2});
      assert.deepEqual(sk.query.toObject("a=true",  {a: false}), {a: true});
      assert.deepEqual(sk.query.toObject("a=true",  {a: "bar"}), {a: "true"});
      assert.deepEqual(sk.query.toObject("a=false", {a: false}), {a: false});
      assert.deepEqual(sk.query.toObject("a=baz",   {a: 2.0}),   {a: NaN});
      assert.deepEqual(sk.query.toObject("a=true&a=false", {a: []}),   {a: ["true", "false"]});
      assert.deepEqual(sk.query.toObject("a=true%20false", {a: []}),   {a: ["true false"]});
      assert.deepEqual(sk.query.toObject("b=1&a=true%20false&b=2.2", {a: [], b:[]}),   {a: ["true false"], b: ["1", "2.2"]});
      assert.deepEqual(sk.query.toObject("a=b%3Dc%253Dfoo%252520bar", {a: {b: {c: ""}}}), {"a": {b: {c: "foo bar"}}});

      assert.deepEqual(sk.query.toObject("a=2&b=true", {a: 1.0, b: false}), {a: 2, b:true});
    }

    function testRoundTrip() {
      var start = {
        a: 2.0,
        b: true,
        c: "foo bar baz",
        d: "default",
        e: ["foo bar", "2"],
        d: ["foo"],
        f: {a: 2.0, b: "foo bar", c: ["a", "b"]},
      };
      var hint = {
        a: 0,
        b: false,
        c: "string",
        e: [],
        d: [],
        f: {a: 1.0, b: "string", c: []},
      };
      assert.deepEqual(sk.query.toObject(sk.query.fromObject(start), hint), start);
    }

    function testDecodeToParamSet() {
      assert.deepEqual(sk.query.toParamSet(),{});
      assert.deepEqual(sk.query.toParamSet(""),{});
      assert.deepEqual(sk.query.toParamSet("a=2"),{a: ["2"]});
      assert.deepEqual(sk.query.toParamSet("a=2&a=3"),{a: ["2", "3"]});
      assert.deepEqual(sk.query.toParamSet("a=2&a=3&b=foo"),{a: ["2", "3"], b: ["foo"]});
      assert.deepEqual(sk.query.toParamSet("a=2%20"),{a: ["2 "]});
    }

    function testEncodeFromParamSet() {
      assert.deepEqual(sk.query.fromParamSet(), "");
      assert.deepEqual(sk.query.fromParamSet({}), "");
      assert.deepEqual(sk.query.fromParamSet({a: ["2"]}), "a=2");
      assert.deepEqual(sk.query.fromParamSet({a: ["2", "3"]}), "a=2&a=3");
      assert.deepEqual(sk.query.fromParamSet({a: ["2", "3"], b: ["foo"]}), "a=2&a=3&b=foo");
      assert.deepEqual(sk.query.fromParamSet({a: ["2 "]}), "a=2%20");
    }

    it('should be able to encode and decode objects.', function() {
      testEncode();
      testDecodeToObject();
      testRoundTrip();
      testDecodeToParamSet();
      testEncodeFromParamSet();
    });
  }
);
