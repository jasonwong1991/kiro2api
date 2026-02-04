## 🤖 Assistant

# CodeWhisperer 流式请求规范 - 完整历史消息示例

## 1. 带完整历史消息的请求体

### 多轮对话历史示例
```json
{
    "conversationState": {
        "conversationId": "a6fd6c62-a966-4748-b34f-d61f33d9bbf2",
        "currentMessage": {
            "userInputMessage": {
                "content": "现在帮我优化这个函数的性能，并创建单元测试",
                "userInputMessageContext": {
                    "editorState": {
                        "document": {
                            "relativeFilePath": "src/calculator.py",
                            "programmingLanguage": {
                                "languageName": "python"
                            },
                            "text": "class Calculator:\n    def add(self, a, b):\n        return a + b\n    \n    def subtract(self, a, b):\n        return a - b\n    \n    def multiply(self, a, b):\n        return a * b\n    \n    def divide(self, a, b):\n        if b != 0:\n            return a / b\n        else:\n            raise ValueError('Cannot divide by zero')\n\nif __name__ == '__main__':\n    calc = Calculator()\n    print('Calculator created successfully!')",
                            "documentSymbols": [
                                {
                                    "name": "Calculator",
                                    "type": "CLASS",
                                    "source": "USER_CODE",
                                    "range": {
                                        "start": {"line": 0, "character": 0},
                                        "end": {"line": 11, "character": 0}
                                    }
                                }
                            ]
                        },
                        "cursorState": [
                            {
                                "range": {
                                    "start": {"line": 8, "character": 0},
                                    "end": {"line": 8, "character": 0}
                                }
                            }
                        ]
                    }
                }
            }
        },
        "history": [
            {
                "messageId": "msg-001",
                "userInputMessage": {
                    "content": "帮我创建一个Python计算器类",
                    "userInputMessageContext": {
                        "editorState": {
                            "document": {
                                "relativeFilePath": "src/calculator.py",
                                "programmingLanguage": {"languageName": "python"},
                                "text": "",
                                "documentSymbols": []
                            },
                            "cursorState": [
                                {
                                    "range": {
                                        "start": {"line": 0, "character": 0},
                                        "end": {"line": 0, "character": 0}
                                    }
                                }
                            ]
                        }
                    }
                }
            },
            {
                "messageId": "msg-002",
                "assistantResponseMessage": {
                    "content": [
                        {
                            "text": "我来帮你创建一个Python计算器类，包含基本的数学运算功能："
                        },
                        {
                            "toolUse": {
                                "toolUseId": "tool-create-001",
                                "name": "create_file",
                                "input": {
                                    "filename": "calculator.py",
                                    "content": "class Calculator:\n    def add(self, a, b):\n        return a + b\n    \n    def subtract(self, a, b):\n        return a - b\n    \n    def multiply(self, a, b):\n        return a * b\n    \n    def divide(self, a, b):\n        if b != 0:\n            return a / b\n        else:\n            raise ValueError('Cannot divide by zero')\n\nif __name__ == '__main__':\n    calc = Calculator()\n    print('Calculator created successfully!')",
                                    "language": "python"
                                }
                            }
                        }
                    ]
                }
            },
            {
                "messageId": "msg-003",
                "toolResultMessage": {
                    "toolResults": [
                        {
                            "toolUseId": "tool-create-001",
                            "content": [
                                {
                                    "text": "文件 calculator.py 创建成功，包含基本的计算器功能"
                                }
                            ],
                            "status": "success"
                        }
                    ]
                }
            },
            {
                "messageId": "msg-004",
                "assistantResponseMessage": {
                    "content": [
                        {
                            "text": "计算器类已经成功创建！这个类包含了四个基本的数学运算方法：\n\n- `add(a, b)`: 加法运算\n- `subtract(a, b)`: 减法运算\n- `multiply(a, b)`: 乘法运算\n- `divide(a, b)`: 除法运算（包含除零检查）\n\n你可以通过实例化Calculator类来使用这些功能。还有什么需要我帮你改进的吗？"
                        }
                    ]
                }
            },
            {
                "messageId": "msg-005",
                "userInputMessage": {
                    "content": "能否添加一些高级数学功能，比如幂运算和开方？",
                    "userInputMessageContext": {
                        "editorState": {
                            "document": {
                                "relativeFilePath": "src/calculator.py",
                                "programmingLanguage": {"languageName": "python"},
                                "text": "class Calculator:\n    def add(self, a, b):\n        return a + b\n    \n    def subtract(self, a, b):\n        return a - b\n    \n    def multiply(self, a, b):\n        return a * b\n    \n    def divide(self, a, b):\n        if b != 0:\n            return a / b\n        else:\n            raise ValueError('Cannot divide by zero')\n\nif __name__ == '__main__':\n    calc = Calculator()\n    print('Calculator created successfully!')",
                                "documentSymbols": [
                                    {
                                        "name": "Calculator",
                                        "type": "CLASS",
                                        "source": "GENERATED_CODE"
                                    }
                                ]
                            }
                        }
                    }
                }
            },
            {
                "messageId": "msg-006",
                "assistantResponseMessage": {
                    "content": [
                        {
                            "text": "当然可以！我来为计算器添加高级数学功能，包括幂运算、开方、对数等："
                        },
                        {
                            "toolUse": {
                                "toolUseId": "tool-update-001",
                                "name": "update_file",
                                "input": {
                                    "filename": "calculator.py",
                                    "content": "import math\n\nclass Calculator:\n    def add(self, a, b):\n        return a + b\n    \n    def subtract(self, a, b):\n        return a - b\n    \n    def multiply(self, a, b):\n        return a * b\n    \n    def divide(self, a, b):\n        if b != 0:\n            return a / b\n        else:\n            raise ValueError('Cannot divide by zero')\n    \n    def power(self, base, exponent):\n        \"\"\"计算幂运算\"\"\"\n        return base ** exponent\n    \n    def square_root(self, number):\n        \"\"\"计算平方根\"\"\"\n        if number < 0:\n            raise ValueError('Cannot calculate square root of negative number')\n        return math.sqrt(number)\n    \n    def nth_root(self, number, n):\n        \"\"\"计算n次方根\"\"\"\n        if n == 0:\n            raise ValueError('Root degree cannot be zero')\n        if number < 0 and n % 2 == 0:\n            raise ValueError('Cannot calculate even root of negative number')\n        return number ** (1/n)\n    \n    def logarithm(self, number, base=math.e):\n        \"\"\"计算对数\"\"\"\n        if number <= 0:\n            raise ValueError('Logarithm undefined for non-positive numbers')\n        if base <= 0 or base == 1:\n            raise ValueError('Invalid logarithm base')\n        return math.log(number, base)\n\nif __name__ == '__main__':\n    calc = Calculator()\n    print('Enhanced Calculator created successfully!')\n    print(f'2^3 = {calc.power(2, 3)}')\n    print(f'√16 = {calc.square_root(16)}')\n    print(f'∛27 = {calc.nth_root(27, 3)}')"
                                }
                            }
                        }
                    ]
                }
            },
            {
                "messageId": "msg-007",
                "toolResultMessage": {
                    "toolResults": [
                        {
                            "toolUseId": "tool-update-001",
                            "content": [
                                {
                                    "text": "文件 calculator.py 更新成功，已添加高级数学功能"
                                }
                            ],
                            "status": "success"
                        }
                    ]
                }
            },
            {
                "messageId": "msg-008",
                "assistantResponseMessage": {
                    "content": [
                        {
                            "text": "太好了！我已经为计算器添加了以下高级数学功能：\n\n**新增功能：**\n- `power(base, exponent)`: 幂运算\n- `square_root(number)`: 平方根\n- `nth_root(number, n)`: n次方根\n- `logarithm(number, base)`: 对数运算（默认自然对数）\n\n**改进点：**\n- 添加了适当的错误处理\n- 包含了详细的文档字符串\n- 添加了使用示例\n\n现在你的计算器功能更加强大了！"
                        }
                    ]
                }
            }
        ]
    },
    "toolConfiguration": {
        "tools": [
            {
                "toolSpec": {
                    "name": "analyze_performance",
                    "description": "分析代码性能并提供优化建议",
                    "inputSchema": {
                        "json": {
                            "type": "object",
                            "properties": {
                                "code": {
                                    "type": "string",
                                    "description": "要分析的代码"
                                },
                                "language": {
                                    "type": "string",
                                    "description": "编程语言"
                                },
                                "focusAreas": {
                                    "type": "array",
                                    "items": {
                                        "type": "string",
                                        "enum": ["memory", "cpu", "io", "algorithms"]
                                    },
                                    "description": "关注的性能领域"
                                }
                            },
                            "required": ["code", "language"]
                        }
                    }
                }
            },
            {
                "toolSpec": {
                    "name": "create_unit_tests",
                    "description": "为代码创建单元测试",
                    "inputSchema": {
                        "json": {
                            "type": "object",
                            "properties": {
                                "code": {
                                    "type": "string",
                                    "description": "要测试的代码"
                                },
                                "testFramework": {
                                    "type": "string",
                                    "enum": ["unittest", "pytest", "nose2"],
                                    "default": "unittest",
                                    "description": "测试框架"
                                },
                                "coverageTarget": {
                                    "type": "number",
                                    "minimum": 0,
                                    "maximum": 100,
                                    "default": 90,
                                    "description": "目标代码覆盖率"
                                }
                            },
                            "required": ["code"]
                        }
                    }
                }
            },
            {
                "toolSpec": {
                    "name": "optimize_code",
                    "description": "优化代码性能",
                    "inputSchema": {
                        "json": {
                            "type": "object",
                            "properties": {
                                "code": {
                                    "type": "string",
                                    "description": "要优化的代码"
                                },
                                "optimizationType": {
                                    "type": "string",
                                    "enum": ["speed", "memory", "readability", "all"],
                                    "description": "优化类型"
                                },
                                "preserveInterface": {
                                    "type": "boolean",
                                    "default": true,
                                    "description": "是否保持接口不变"
                                }
                            },
                            "required": ["code"]
                        }
                    }
                }
            }
        ]
    },
    "inferenceConfiguration": {
        "maxTokens": 4000,
        "temperature": 0.2,
        "topP": 0.9
    }
}
```

## 2. 基于历史的流式响应示例

### 完整的多工具调用流式响应
```
event: messageStart
data: {"messageId": "msg-009", "conversationId": "a6fd6c62-a966-4748-b34f-d61f33d9bbf2"}

event: contentBlockStart
data: {"contentBlockIndex": 0, "contentBlock": {"text": ""}}

event: contentBlockDelta
data: {"delta": {"text": "基于我们之前创建和增强的计算器类，我现在来帮你进行性能优化并创建全面的单元测试。\n\n首先让我分析当前代码的性能："}, "contentBlockIndex": 0}

event: contentBlockStop
data: {"contentBlockIndex": 0}

event: contentBlockStart
data: {"contentBlockIndex": 1, "contentBlock": {"toolUse": {"toolUseId": "perf-analysis-001", "name": "analyze_performance"}}}

event: contentBlockDelta
data: {"delta": {"toolUse": {"input": "{\"code\": \"import math\\n\\nclass Calculator:\\n    def add(self, a, b):\\n        return a + b\\n    \\n    def subtract(self, a, b):\\n        return a - b\\n    \\n    def multiply(self, a, b):\\n        return a * b\\n    \\n    def divide(self, a, b):\\n        if b != 0:\\n            return a / b\\n        else:\\n            raise ValueError('Cannot divide by zero')\\n    \\n    def power(self, base, exponent):\\n        return base ** exponent\\n    \\n    def square_root(self, number):\\n        if number < 0:\\n            raise ValueError('Cannot calculate square root of negative number')\\n        return math.sqrt(number)\\n    \\n    def nth_root(self, number, n):\\n        if n == 0:\\n            raise ValueError('Root degree cannot be zero')\\n        if number < 0 and n % 2 == 0:\\n            raise ValueError('Cannot calculate even root of negative number')\\n        return number ** (1/n)\\n    \\n    def logarithm(self, number, base=math.e):\\n        if number <= 0:\\n            raise ValueError('Logarithm undefined for non-positive numbers')\\n        if base <= 0 or base == 1:\\n            raise ValueError('Invalid logarithm base')\\n        return math.log(number, base)\", \"language\": \"python\", \"focusAreas\": [\"memory\", \"cpu\", \"algorithms\"]}"}}, "contentBlockIndex": 1}

event: contentBlockStop
data: {"contentBlockIndex": 1}

event: messageStop
data: {"stopReason": "tool_use"}

event: messageStart
data: {"messageId": "msg-010", "conversationId": "a6fd6c62-a966-4748-b34f-d61f33d9bbf2"}

event: contentBlockStart
data: {"contentBlockIndex": 0, "contentBlock": {"text": ""}}

event: contentBlockDelta
data: {"delta": {"text": "根据性能分析结果，现在让我创建优化版本的代码："}, "contentBlockIndex": 0}

event: contentBlockStop
data: {"contentBlockIndex": 0}

event: contentBlockStart
data: {"contentBlockIndex": 1, "contentBlock": {"toolUse": {"toolUseId": "optimize-001", "name": "optimize_code"}}}

event: contentBlockDelta
data: {"delta": {"toolUse": {"input": "{\"code\": \"import math\\nfrom functools import lru_cache\\nfrom typing import Union, Optional\\n\\nclass Calculator:\\n    \\\"\\\"\\\"高性能计算器类，支持基本和高级数学运算\\\"\\\"\\\"\\n    \\n    def __init__(self):\\n        # 预计算常用值以提高性能\\n        self._common_roots = {2: math.sqrt, 3: lambda x: x**(1/3)}\\n        self._log_bases = {10: math.log10, 2: math.log2}\\n    \\n    def add(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        \\\"\\\"\\\"加法运算\\\"\\\"\\\"\\n        return a + b\\n    \\n    def subtract(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        \\\"\\\"\\\"减法运算\\\"\\\"\\\"\\n        return a - b\\n    \\n    def multiply(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        \\\"\\\"\\\"乘法运算\\\"\\\"\\\"\\n        return a * b\\n    \\n    def divide(self, a: Union[int, float], b: Union[int, float]) -> float:\\n        \\\"\\\"\\\"除法运算，包含除零检查\\\"\\\"\\\"\\n        if b == 0:\\n            raise ValueError('Cannot divide by zero')\\n        return a / b\\n    \\n    def power(self, base: Union[int, float], exponent: Union[int, float]) -> Union[int, float]:\\n        \\\"\\\"\\\"幂运算，优化整数指数情况\\\"\\\"\\\"\\n        if isinstance(exponent, int) and exponent >= 0:\\n            # 对于非负整数指数，使用更快的整数幂运算\\n            return pow(base, exponent)\\n        return base ** exponent\\n    \\n    def square_root(self, number: Union[int, float]) -> float:\\n        \\\"\\\"\\\"平方根运算，优化性能\\\"\\\"\\\"\\n        if number < 0:\\n            raise ValueError('Cannot calculate square root of negative number')\\n        if number == 0:\\n            return 0.0\\n        if number == 1:\\n            return 1.0\\n        return math.sqrt(number)\\n    \\n    @lru_cache(maxsize=128)\\n    def nth_root(self, number: Union[int, float], n: int) -> float:\\n        \\\"\\\"\\\"n次方根运算，使用缓存优化\\\"\\\"\\\"\\n        if n == 0:\\n            raise ValueError('Root degree cannot be zero')\\n        if number < 0 and n % 2 == 0:\\n            raise ValueError('Cannot calculate even root of negative number')\\n        \\n        # 使用预计算的常用根\\n        if n in self._common_roots:\\n            return self._common_rootsn [<sup>1</sup>](number)\\n        \\n        return number ** (1/n)\\n    \\n    def logarithm(self, number: Union[int, float], base: Optional[Union[int, float]] = None) -> float:\\n        \\\"\\\"\\\"对数运算，优化常用底数\\\"\\\"\\\"\\n        if number <= 0:\\n            raise ValueError('Logarithm undefined for non-positive numbers')\\n        \\n        if base is None:\\n            return math.log(number)\\n        \\n        if base <= 0 or base == 1:\\n            raise ValueError('Invalid logarithm base')\\n        \\n        # 使用预计算的常用底数\\n        if base in self._log_bases:\\n            return self._log_bases[base](number)\\n        \\n        return math.log(number, base)\\n    \\n    def batch_calculate(self, operation: str, numbers: list) -> list:\\n        \\\"\\\"\\\"批量计算，提高处理大量数据的效率\\\"\\\"\\\"\\n        operations = {\\n            'square': lambda x: x * x,\\n            'sqrt': self.square_root,\\n            'log': lambda x: self.logarithm(x),\\n            'abs': abs\\n        }\\n        \\n        if operation not in operations:\\n            raise ValueError(f'Unsupported batch operation: {operation}')\\n        \\n        return [operationsoperation [<sup>2</sup>](num) for num in numbers]\", \"optimizationType\": \"all\", \"preserveInterface\": true}"}}, "contentBlockIndex": 1}

event: contentBlockStop
data: {"contentBlockIndex": 1}

event: contentBlockStart
data: {"contentBlockIndex": 2, "contentBlock": {"text": ""}}

event: contentBlockDelta
data: {"delta": {"text": "\\n\\n现在让我为优化后的计算器创建全面的单元测试："}, "contentBlockIndex": 2}

event: contentBlockStop
data: {"contentBlockIndex": 2}

event: contentBlockStart
data: {"contentBlockIndex": 3, "contentBlock": {"toolUse": {"toolUseId": "test-creation-001", "name": "create_unit_tests"}}}

event: contentBlockDelta
data: {"delta": {"toolUse": {"input": "{\"code\": \"import math\\nfrom functools import lru_cache\\nfrom typing import Union, Optional\\n\\nclass Calculator:\\n    def __init__(self):\\n        self._common_roots = {2: math.sqrt, 3: lambda x: x**(1/3)}\\n        self._log_bases = {10: math.log10, 2: math.log2}\\n    \\n    def add(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        return a + b\\n    \\n    def subtract(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        return a - b\\n    \\n    def multiply(self, a: Union[int, float], b: Union[int, float]) -> Union[int, float]:\\n        return a * b\\n    \\n    def divide(self, a: Union[int, float], b: Union[int, float]) -> float:\\n        if b == 0:\\n            raise ValueError('Cannot divide by zero')\\n        return a / b\\n    \\n    def power(self, base: Union[int, float], exponent: Union[int, float]) -> Union[int, float]:\\n        if isinstance(exponent, int) and exponent >= 0:\\n            return pow(base, exponent)\\n        return base ** exponent\\n    \\n    def square_root(self, number: Union[int, float]) -> float:\\n        if number < 0:\\n            raise ValueError('Cannot calculate square root of negative number')\\n        if number == 0:\\n            return 0.0\\n        if number == 1:\\n            return 1.0\\n        return math.sqrt(number)\\n    \\n    @lru_cache(maxsize=128)\\n    def nth_root(self, number: Union[int, float], n: int) -> float:\\n        if n == 0:\\n            raise ValueError('Root degree cannot be zero')\\n        if number < 0 and n % 2 == 0:\\n            raise ValueError('Cannot calculate even root of negative number')\\n        if n in self._common_roots:\\n            return self._common_rootsn [<sup>1</sup>](number)\\n        return number ** (1/n)\\n    \\n    def logarithm(self, number: Union[int, float], base: Optional[Union[int, float]] = None) -> float:\\n        if number <= 0:\\n            raise ValueError('Logarithm undefined for non-positive numbers')\\n        if base is None:\\n            return math.log(number)\\n        if base <= 0 or base == 1:\\n            raise ValueError('Invalid logarithm base')\\n        if base in self._log_bases:\\n            return self._log_bases[base](number)\\n        return math.log(number, base)\\n    \\n    def batch_calculate(self, operation: str, numbers: list) -> list:\\n        operations = {\\n            'square': lambda x: x * x,\\n            'sqrt': self.square_root,\\n            'log': lambda x: self.logarithm(x),\\n            'abs': abs\\n        }\\n        if operation not in operations:\\n            raise ValueError(f'Unsupported batch operation: {operation}')\\n        return [operationsoperation [<sup>2</sup>](num) for num in numbers]\", \"testFramework\": \"unittest\", \"coverageTarget\": 95}"}}, "contentBlockIndex": 3}

event: contentBlockStop
data: {"contentBlockIndex": 3}

event: messageStop
data: {"stopReason": "tool_use"}
```

## 3. 工具调用结果处理（带历史上下文）

### 工具结果响应请求
```json
{
 "conversationState": {
 "conversationId": "a6fd6c62-a966-4748-b34f-d61f33d9bbf2",
 "currentMessage": {
 "toolResultMessage": {
 "toolResults": [
 {
 "toolUseId": "perf-analysis-001",
 "content": [
 {
 "text": "性能分析结果：\n\n**优化建议：**\n1. 添加类型注解提高性能\n2. 使用缓存优化重复计算\n3. 预计算常用值\n4. 优化条件检查顺序\n5. 添加批量处理功能\n\n**当前性能评分：** 7.5/10\n**优化后预期评分：** 9.2/10"
 }
 ],
 "status": "success"
 },
 {
 "toolUseId": "optimize-001",
 "content": [
 {
 "text": "代码优化完成！主要改进：\n\n1. **性能优化：**\n - 添加了 @lru_cache 装饰器缓存计算结果\n - 预计算常用根和对数底数\n - 优化了幂运算的整数情况\n - 添加了批量计算功能\n\n2. **代码质量：**\n - 添加了完整的类型注解\n - 改进了错误处理\n - 增加了详细的文档字符串\n\n3. **新功能：**\n - batch_calculate() 方法支持批量处理\n - 智能缓存机制\n - 优化的常用值处理"
 }
 ],
 "status": "success"
 },
 {
 "toolUseId": "test-creation-001",
 "content": [
 {
 "text": "单元测试创建完成！\n\n**测试覆盖范围：**\n- 基本运算测试（加减乘除）\n- 高级运算测试（幂、根、对数）\n- 边界条件测试\n- 异常处理测试\n- 性能测试\n- 批量操作测试\n\n**预期代码覆盖率：** 96%\n**测试用例数量：** 45个\n\n测试文件已生成：test_calculator.py"
 }
 ],
 "status": "success"
 }
 ]
 }
 },
 "history": [
 // ... 前面的完整历史消息 ...
 {
 "messageId": "msg-009",
 "assistantResponseMessage": {
 "content": [
 {
 "text": "基于我们之前创建和增强的计算器类，我现在来帮你进行性能优化并创建全面的单元测试。\n\n首先让我分析当前代码的性能："
 },
 {
 "toolUse": {
 "toolUseId": "perf-analysis-001",
 "name": "analyze_performance",
 "input": {
 "code": "import math\n\nclass Calculator:\n def add(self, a, b):\n return a + b\n \n def subtract(self, a, b):\n return a - b\n \n def multiply(self, a, b):\n return a * b\n \n def divide(self, a, b):\n if b != 0:\n return a / b\n else:\n raise ValueError('Cannot divide by zero')\n \n def power(self, base, exponent):\n return base ** exponent\n \n def square_root(self, number):\n if number < 0:\n raise ValueError('Cannot calculate square root of negative number')\n return math.sqrt(number)\n \n def nth_root(self, number, n):\n if n == 0:\n raise ValueError('Root degree cannot be zero')\n if number < 0 and n % 2 == 0:\n raise ValueError('Cannot calculate even root of negative number')\n return number ** (1/n)\n \n def logarithm(self, number, base=math.e):\n if number <= 0:\n raise ValueError('Logarithm undefined for non-positive numbers')\n if base <= 0 or base == 1:\n raise ValueError('Invalid logarithm base')\n return math.log(number, base)",
 "language": "python",
 "focusAreas": ["memory", "cpu", "algorithms"]
 }
 }
 },
 {
 "toolUse": {
 "toolUseId": "optimize-001",
 "name": "optimize_code",
 "input": {
 "code": "...",
 "optimizationType
