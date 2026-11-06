import {Link} from 'react-router-dom';
import {useState} from 'react';
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query';
import {Loader2, RotateCcw, Save} from 'lucide-react';
import {toast} from 'sonner';
import {PageHeader} from '@/components/PageHeader';
import QueryError from '@/components/QueryError';
import {Button} from '@/components/ui/button';
import {Card, CardContent, CardDescription, CardHeader, CardTitle} from '@/components/ui/card';
import {Textarea} from '@/components/ui/textarea';
import {useDevice} from '@/context/DeviceContext';
import {deviceKey} from '@/api/devices';
import {getTTSText, saveTTSText} from '@/api/property';

const MAX_BYTES = 2000;

const utf8Bytes = (value: string) => new TextEncoder().encode(value).length;

export default function TTSSettings() {
    const {deviceId, device, isActive} = useDevice();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState<string | null>(null);

    const textQuery = useQuery({
        queryKey: deviceKey(deviceId, 'ttsText'),
        queryFn: ({signal}) => getTTSText(deviceId, signal),
    });

    const saveMutation = useMutation({
        mutationFn: (text: string) => saveTTSText(deviceId, text),
        onSuccess: async (result) => {
            if (!isActive()) return;
            await queryClient.invalidateQueries({queryKey: deviceKey(deviceId, 'ttsText')});
            if (result.pushed) toast.success('文案已保存，并已下发到设备');
            else toast.warning(result.message ? `文案已保存，但下发失败：${result.message}` : '文案已保存，但下发失败');
        },
        onError: (error: Error) => toast.error(error.message),
    });

    if (textQuery.isError) return <QueryError error={textQuery.error} retry={() => textQuery.refetch()}/>;

    const text = draft ?? textQuery.data ?? '';
    const bytes = utf8Bytes(text);
    const tooLong = bytes > MAX_BYTES;
    const changed = draft !== null && draft !== textQuery.data;
    const model = device.features?.model ?? '';
    const unsupported = model !== '' && !model.startsWith('Air724');

    return <div className="space-y-6">
        <PageHeader
            title="来电播报"
            description={`配置设备自动接听来电时向对方播报的 TTS 文案（当前设备：${device.name || deviceId}）`}
        />

        <Card>
            <CardHeader>
                <CardTitle>播报文案</CardTitle>
                <CardDescription>
                    保存后由服务端通过 MQTT 下发 <code>set_tts</code> 命令，设备写入闪存，重启后仍然生效；
                    设备离线时会先保存配置，等设备下次上线自动补发。
                </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
                {textQuery.isPending
                    ? <div className="flex items-center gap-2 text-sm text-slate-500"><Loader2 className="h-4 w-4 animate-spin"/>正在读取配置…</div>
                    : <>
                        {unsupported && <p className="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-700">
                            当前设备报告的型号为 {model}，只有 Air724UG 脚本能处理该命令；下发后设备可能回复“unknown command”。
                        </p>}
                        <Textarea
                            value={text}
                            onChange={(event) => setDraft(event.target.value)}
                            placeholder="留空表示使用设备固件内置的默认文案"
                            rows={6}
                            maxLength={MAX_BYTES * 3}
                            aria-label="播报文案"
                        />
                        <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-slate-500">
                            <span className={tooLong ? 'text-rose-600' : ''}>{bytes} / {MAX_BYTES} 字节（UTF-8）</span>
                            <span>留空 = 恢复固件默认文案</span>
                        </div>
                        <div className="flex flex-wrap gap-2">
                            <Button
                                type="button"
                                onClick={() => draft !== null && saveMutation.mutate(draft)}
                                disabled={!changed || tooLong || saveMutation.isPending}
                            >
                                {saveMutation.isPending
                                    ? <><Loader2 className="mr-2 h-4 w-4 animate-spin"/>保存中…</>
                                    : <><Save className="mr-2 h-4 w-4"/>保存并下发</>}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                onClick={() => {
                                    setDraft('');
                                    saveMutation.mutate('');
                                }}
                                disabled={tooLong || saveMutation.isPending || text === ''}
                            >
                                <RotateCcw className="mr-2 h-4 w-4"/>恢复固件默认
                            </Button>
                        </div>
                    </>}
            </CardContent>
        </Card>

        <Card>
            <CardHeader>
                <CardTitle>说明</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2 text-sm text-slate-600">
                <p>仅 Air724UG 设备支持语音播报；设备需使用带 TTS 的固件，并且 <code>auto_answer.enabled</code> 为 true 才会自动接听。</p>
                <p>文案只影响之后接听的通话，正在进行的通话不会被中断。</p>
                <p>设备端的固件默认值仍可在 <code>Air724UG/script/device_config.lua</code> 中修改。</p>
                <p className="text-xs text-slate-500">下发结果可在 <Link className="underline" to={`/devices/${encodeURIComponent(deviceId)}/serial`}>设备控制</Link> 的命令记录中查看。</p>
            </CardContent>
        </Card>
    </div>;
}
